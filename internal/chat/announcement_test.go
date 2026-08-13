package chat

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

// statements records every query a pool runs, so "this round trip no longer
// happens" can be asserted rather than assumed.
type statements struct {
	mu   sync.Mutex
	seen []string
}

func (s *statements) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	s.mu.Lock()
	s.seen = append(s.seen, strings.Join(strings.Fields(data.SQL), " "))
	s.mu.Unlock()
	return ctx
}

func (s *statements) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (s *statements) matching(fragment string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var hits []string
	for _, q := range s.seen {
		if strings.Contains(q, fragment) {
			hits = append(hits, q)
		}
	}
	return hits
}

func (s *statements) reset() {
	s.mu.Lock()
	s.seen = nil
	s.mu.Unlock()
}

// tracedService is a chat service whose pool reports the statements it runs.
func tracedService(t *testing.T) (*Service, *pgxpool.Pool, *statements) {
	t.Helper()
	pool := testsupport.DB(t)

	log := &statements{}
	cfg := pool.Config().Copy()
	cfg.ConnConfig.Tracer = log
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(traced.Close)
	return NewService(traced, nil), pool, log
}

// announcementNotifier records what NotifyMany was asked to send.
type announcementNotifier struct {
	mu         sync.Mutex
	calls      int
	recipients []uuid.UUID
	title      string
	body       string
}

func (a *announcementNotifier) Notify(context.Context, uuid.UUID, uuid.UUID, string, string, string, map[string]any) error {
	return nil
}

func (a *announcementNotifier) NotifyMany(_ context.Context, _ uuid.UUID, userIDs []uuid.UUID, _, title, body string, _ map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	a.recipients = append(a.recipients, userIDs...)
	a.title = title
	a.body = body
	return nil
}

func (a *announcementNotifier) PushToUsers(context.Context, []uuid.UUID, protocol.Push) {}

func (a *announcementNotifier) snapshot() (int, []uuid.UUID, string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, append([]uuid.UUID(nil), a.recipients...), a.title, a.body
}

// Every message ran the announcement-recipient lookup, joining channel_members
// to channels only to discover the channel is not an announcement channel and
// throw the answer away. That is roughly 95% of messages paying for a round
// trip that returns nothing — and the message insert had already read the
// channel row it needed to know better.
func TestAnOrdinaryMessageDoesNotAskWhoToTellAboutAnAnnouncement(t *testing.T) {
	svc, pool, log := tracedService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "colleague@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, other)
	svc.SetNotifier(&announcementNotifier{})

	log.reset()
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "morning", nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}

	if hits := log.matching("c.type = 'announcement'"); len(hits) != 0 {
		t.Errorf("an ordinary message still ran the announcement lookup %d time(s):\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// And the case it exists for still works: an announcement reaches everybody in
// the channel but its author.
func TestAnAnnouncementStillNotifiesTheWholeChannel(t *testing.T) {
	svc, pool, _ := tracedService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reader := testsupport.SeedUser(t, pool, org, "reader@example.test", "member")

	// A second organization, whose members must not be told.
	theirs := testsupport.SeedOrg(t, pool)

	name := "company-news"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "announcement", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	notifier := &announcementNotifier{}
	svc.SetNotifier(notifier)

	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID,
		"we are moving office on Friday", nil, nil, true); err != nil {
		t.Fatalf("send: %v", err)
	}

	calls, recipients, title, body := notifier.snapshot()
	if calls != 1 {
		t.Fatalf("NotifyMany was called %d times, want 1", calls)
	}
	if len(recipients) != 1 || recipients[0] != reader {
		t.Errorf("recipients: got %v, want just %s", recipients, reader)
	}
	for _, r := range recipients {
		if r == org.OwnerID {
			t.Error("the author was told about their own announcement")
		}
		if r == theirs.OwnerID {
			t.Error("another organization was told about this announcement")
		}
	}
	// The channel names the notification, and the message is its body.
	if title != name {
		t.Errorf("title: got %q want %q", title, name)
	}
	if !strings.Contains(body, "moving office") {
		t.Errorf("body: got %q", body)
	}
}
