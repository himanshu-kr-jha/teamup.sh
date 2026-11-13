package notifications

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/testsupport"
)

// channelSize is a busy channel at a company this product is built for. Every
// message posted in one costs whatever the fan-out below costs, times this.
const channelSize = 60

// sqlCounter counts the statements a pool actually sends, so "one query
// instead of one per person" can be asserted rather than assumed.
type sqlCounter struct {
	mu    sync.Mutex
	stmts []string
}

func (c *sqlCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	c.stmts = append(c.stmts, data.SQL)
	c.mu.Unlock()
	return ctx
}

func (c *sqlCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// matching counts the statements mentioning a fragment, and returns them so a
// failure can show what was actually run.
func (c *sqlCounter) matching(fragment string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var hits []string
	for _, s := range c.stmts {
		if strings.Contains(s, fragment) {
			hits = append(hits, strings.Join(strings.Fields(s), " "))
		}
	}
	return hits
}

func (c *sqlCounter) reset() {
	c.mu.Lock()
	c.stmts = nil
	c.mu.Unlock()
}

// tracedService is the service reading through a pool that reports every
// statement it runs.
func tracedService(t *testing.T) (*Service, *pgxpool.Pool, *sqlCounter) {
	t.Helper()
	pool := testsupport.DB(t)

	counter := &sqlCounter{}
	cfg := pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(traced.Close)

	svc := NewService(traced, nil, Config{PushServiceHosts: []string{"127.0.0.1", "push.example.test"}})
	public, private, err := auth.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	svc.SetVAPIDKeys(public, private)
	return svc, pool, counter
}

// pushEndpoint is a stand-in push service that records what reached it and how
// many connections were opened to get there.
type pushEndpoint struct {
	server *httptest.Server

	mu       sync.Mutex
	requests int
	conns    int
}

func newPushEndpoint(t *testing.T) *pushEndpoint {
	t.Helper()
	p := &pushEndpoint{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		p.requests++
		p.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	// Every accepted connection is a TCP handshake, and against a real push
	// service a TLS one too.
	p.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state != http.StateNew {
			return
		}
		p.mu.Lock()
		p.conns++
		p.mu.Unlock()
	}
	t.Cleanup(p.server.Close)
	return p
}

func (p *pushEndpoint) counts() (requests, conns int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests, p.conns
}

// seedSubscribers puts one subscribed device on each of n people.
func seedSubscribers(t *testing.T, svc *Service, pool *pgxpool.Pool, org *testsupport.Org, endpoint string, n int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ids := []uuid.UUID{org.OwnerID}
	for i := 0; i < n-1; i++ {
		ids = append(ids, testsupport.SeedUser(t, pool, org, fmt.Sprintf("member%d@example.test", i), "member"))
	}
	for i, id := range ids {
		if err := svc.SaveSubscription(ctx, id.String(), org.ID.String(),
			realSubscription(t, fmt.Sprintf("%s/send/%d", endpoint, i))); err != nil {
			t.Fatalf("save subscription: %v", err)
		}
	}
	return ids
}

// Every message in a busy channel fanned out one subscription query per
// recipient: a 60-person channel cost 60 round trips to the database before a
// single push had been sent, all of them the same statement with a different
// id, all of them competing for the same pool as user traffic.
func TestAChannelFanOutReadsSubscriptionsInOneQuery(t *testing.T) {
	svc, pool, counter := tracedService(t)
	ctx := context.Background()
	endpoint := newPushEndpoint(t)
	org := testsupport.SeedOrg(t, pool)

	ids := seedSubscribers(t, svc, pool, org, endpoint.server.URL, channelSize)

	counter.reset()
	svc.pushToUsers(ctx, ids, PushPayload{Title: "#general", Body: "somebody said something"})

	queries := counter.matching("FROM push_subscriptions")
	if len(queries) != 1 {
		t.Errorf("pushing to %d people ran %d subscription queries, want 1:\n  %s",
			len(ids), len(queries), strings.Join(queries, "\n  "))
	}

	// Batching is only worth anything if everybody still gets their push.
	delivered, _ := endpoint.counts()
	if delivered != len(ids) {
		t.Errorf("delivered %d pushes, want %d", delivered, len(ids))
	}
}

// Push delivery is a burst: a channel's worth of devices, all to the same
// handful of push services, several times a second in a busy conversation.
//
// A client was built per push. That pooled connections in spite of itself —
// a client with no Transport uses the shared http.DefaultTransport — but
// DefaultTransport keeps only two idle connections per host, so everything
// past the second in a burst has its connection closed on completion and the
// next burst pays for the handshakes again. Against a real push service each
// of those is a TLS handshake.
func TestPushDeliveryKeepsItsConnectionsBetweenBursts(t *testing.T) {
	svc, pool, _ := tracedService(t)
	ctx := context.Background()
	endpoint := newPushEndpoint(t)
	org := testsupport.SeedOrg(t, pool)

	ids := seedSubscribers(t, svc, pool, org, endpoint.server.URL, channelSize)

	// The first burst opens whatever it needs; it is the second that shows
	// whether anything was kept.
	svc.pushToUsers(ctx, ids, PushPayload{Title: "#general", Body: "first"})
	_, afterWarmUp := endpoint.counts()

	svc.pushToUsers(ctx, ids, PushPayload{Title: "#general", Body: "second"})
	delivered, total := endpoint.counts()
	opened := total - afterWarmUp

	if delivered != 2*len(ids) {
		t.Fatalf("delivered %d pushes, want %d", delivered, 2*len(ids))
	}
	// A handful is fine — the pool is bounded and a burst can briefly want more
	// than it holds. One per push is the handshake storm.
	if opened > len(ids)/4 {
		t.Errorf("a second burst of %d pushes opened %d new connections;"+
			" the first burst's %d were not kept", len(ids), opened, afterWarmUp)
	}
}

// A person with TeamUp open on the laptop on their desk was skipped altogether,
// because they had a socket, so the phone in their pocket never rang and never
// showed a message. Every device of theirs has to be reached; each one's
// service worker decides whether it needs to show anything.
func TestEveryDeviceIsReachedWhateverElseThePersonHasOpen(t *testing.T) {
	svc, pool, _ := tracedService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	var mu sync.Mutex
	reached := map[string]http.Header{}
	push := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reached[r.URL.Path] = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(push.Close)

	for _, device := range []string{"laptop", "phone"} {
		if err := svc.SaveSubscription(ctx, org.OwnerID.String(), org.ID.String(),
			realSubscription(t, push.URL+"/"+device)); err != nil {
			t.Fatalf("save subscription: %v", err)
		}
	}

	svc.pushToUsers(ctx, []uuid.UUID{org.OwnerID}, PushPayload{Title: "Asha", Body: "Incoming call", TTL: 45})

	mu.Lock()
	defer mu.Unlock()
	for _, device := range []string{"/laptop", "/phone"} {
		header, ok := reached[device]
		if !ok {
			t.Errorf("the %s was never reached", device[1:])
			continue
		}
		// A phone asleep holds back anything less than high until it next wakes.
		if got := header.Get("Urgency"); got != "high" {
			t.Errorf("%s: Urgency %q, want high", device[1:], got)
		}
		if got := header.Get("TTL"); got != "45" {
			t.Errorf("%s: TTL %q, want the 45 seconds asked for", device[1:], got)
		}
	}
}

// The batched lookup must not hand one organization's devices to another's
// fan-out, and must keep every device a person has registered.
func TestBatchedSubscriptionLookupKeepsPeopleApart(t *testing.T) {
	svc, pool, _ := tracedService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	laptop := realSubscription(t, "https://push.example.test/laptop")
	phone := realSubscription(t, "https://push.example.test/phone")
	for _, sub := range []*PushSubscription{laptop, phone} {
		if err := svc.SaveSubscription(ctx, org.OwnerID.String(), org.ID.String(), sub); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if err := svc.SaveSubscription(ctx, theirs.OwnerID.String(), theirs.ID.String(),
		realSubscription(t, "https://push.example.test/theirs")); err != nil {
		t.Fatalf("save: %v", err)
	}

	byUser, err := svc.subscriptionsFor(ctx, []uuid.UUID{org.OwnerID})
	if err != nil {
		t.Fatalf("batched lookup: %v", err)
	}
	if got := len(byUser[org.OwnerID]); got != 2 {
		t.Errorf("both of one person's devices should come back, got %d", got)
	}
	if _, ok := byUser[theirs.OwnerID]; ok {
		t.Error("a user who was not asked for came back from the lookup")
	}
	for _, sub := range byUser[org.OwnerID] {
		if sub.OrgID != org.ID {
			t.Errorf("subscription %s carries org %s, want %s", sub.Endpoint, sub.OrgID, org.ID)
		}
	}
}
