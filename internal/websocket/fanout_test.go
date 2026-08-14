package websocket

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/pkg/protocol"
)

// These tests stand up two hubs in one test binary. That is the only shape in
// which the defect is visible: a single-process test passes whether or not
// anything crosses between processes, because there is only ever one map to
// look in.
//
// The two hubs share nothing but Redis, which is exactly what two replicas of
// the server are.

// EnvRedisURL names the Redis to test against; the default matches the one the
// development stack runs.
const EnvRedisURL = "TEAMUP_TEST_REDIS_URL"

func redisURL() string {
	if url := os.Getenv(EnvRedisURL); url != "" {
		return url
	}
	return "redis://localhost:6379"
}

// testRedis returns a client of its own, so that two hubs in one test are as
// separate as two processes would be.
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	return testRedisAt(t, redisURL())
}

func testRedisAt(t *testing.T, url string) *redis.Client {
	t.Helper()

	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", EnvRedisURL, url, err)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Skipf("set %s to a reachable Redis to run the fan-out tests: %v", EnvRedisURL, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// twoProcesses returns two hubs that share nothing but Redis.
func twoProcesses(t *testing.T) (*Hub, *Hub) {
	t.Helper()

	// A pub/sub channel of this test's own. Every process subscribes to one
	// channel, so without this every test in the binary would be delivering
	// into every other one.
	channel := "teamup:test:fanout:" + uuid.NewString()
	return processOn(t, testRedis(t), channel), processOn(t, testRedis(t), channel)
}

// processOn returns a hub wired for cross-process delivery and already
// subscribed, which is what a running replica is.
func processOn(t *testing.T, client *redis.Client, channel string) *Hub {
	t.Helper()

	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	fanout := NewFanout(client, zerolog.Nop())
	fanout.channel = channel
	hub.SetFanout(fanout)
	fanout.Start()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = fanout.Close(ctx)
	})

	// Pub/sub has no backlog: publishing before the subscription is registered
	// delivers to nobody, which would make every test here a coin toss.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fanout.WaitReady(ctx); err != nil {
		t.Fatalf("the fan-out never subscribed: %v", err)
	}
	return hub
}

// join opens a connection on one hub. The buffer is generous because presence
// announcements from the other process arrive on the same channel, and a test
// asserting about a chat message should not fail because of them.
func join(t *testing.T, hub *Hub, id, userID, orgID string) *Connection {
	t.Helper()

	conn := testConnection(hub, id, userID, orgID)
	conn.SendCh = make(chan *protocol.Message, 64)
	conn.logger = zerolog.Nop()
	hub.Register(conn)
	return conn
}

// expect waits for one message of a type, stepping over anything else that
// happens to be queued.
func expect(t *testing.T, conn *Connection, msgType string) *protocol.Message {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-conn.SendCh:
			if msg.Type == msgType {
				return msg
			}
		case <-deadline:
			t.Fatalf("%s never received %s", conn.ID, msgType)
			return nil
		}
	}
}

// expectNothing fails if a message of a type turns up within the window.
func expectNothing(t *testing.T, conn *Connection, msgType string, within time.Duration) {
	t.Helper()

	deadline := time.After(within)
	for {
		select {
		case msg := <-conn.SendCh:
			if msg.Type == msgType {
				t.Fatalf("%s received %s and should not have", conn.ID, msgType)
			}
		case <-deadline:
			return
		}
	}
}

// payloadField reads one string out of a message's payload.
func payloadField(t *testing.T, msg *protocol.Message, field string) string {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(msg.Payload, &body); err != nil {
		t.Fatalf("decode %s payload: %v", msg.Type, err)
	}
	value, _ := body[field].(string)
	return value
}

// A message sent by somebody whose socket is on one replica reaches only the
// subset of the channel connected to that same replica. Everybody else sees
// nothing until they reload, and there is no error, no log line and no metric
// to say so.
func TestAChannelMessageReachesConnectionsOnAnotherProcess(t *testing.T) {
	a, b := twoProcesses(t)
	roster := stubRoster{members: []string{"user-a", "user-b"}}
	a.SetChannelRoster(roster)
	b.SetChannelRoster(roster)

	// The sender's own replica has a connection too, so this is a real
	// two-replica channel rather than a one-sided one.
	join(t, a, "conn-a", "user-a", "org-1")
	there := join(t, b, "conn-b", "user-b", "org-1")

	a.BroadcastToChannel("channel-1",
		protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "hello"}), "user-a")

	expect(t, there, protocol.TypeChatMessage)
}

// Ringing somebody, or telling them about a notification, goes to every socket
// they have open — including the ones on another replica.
func TestADirectMessageReachesTheirSocketsOnAnotherProcess(t *testing.T) {
	a, b := twoProcesses(t)

	join(t, a, "conn-here", "user-1", "org-1")
	there := join(t, b, "conn-there", "user-1", "org-1")

	a.BroadcastToUser("user-1", protocol.NewMessage(protocol.TypeCallInvited, map[string]string{"callId": "abc"}))

	expect(t, there, protocol.TypeCallInvited)
}

// Presence is a shared view or it is worthless: somebody connected to one
// replica has to show online to a colleague on another.
func TestPresenceChangesAreSeenOnEveryProcess(t *testing.T) {
	a, b := twoProcesses(t)

	watcher := join(t, b, "conn-watcher", "user-watcher", "org-1")
	drain(watcher)

	// Somebody connecting on the other replica is the event that has to travel.
	join(t, a, "conn-newcomer", "user-newcomer", "org-1")

	msg := expect(t, watcher, protocol.TypePresenceChanged)
	if got := payloadField(t, msg, "userId"); got != "user-newcomer" {
		t.Errorf("presence was announced for the wrong person: %q", got)
	}
}

// The process that published must deliver locally exactly once: not twice by
// also acting on its own publication, and not zero times by leaving delivery to
// a subscriber that skips it.
func TestTheOriginatingProcessDeliversExactlyOnce(t *testing.T) {
	a, b := twoProcesses(t)
	roster := stubRoster{members: []string{"user-a", "user-b"}}
	a.SetChannelRoster(roster)
	b.SetChannelRoster(roster)

	here := join(t, a, "conn-a", "user-a", "org-1")
	there := join(t, b, "conn-b", "user-b", "org-1")
	drain(here)
	drain(there)

	a.BroadcastToChannel("channel-1",
		protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "hello"}), "")

	expect(t, here, protocol.TypeChatMessage)
	// Wait until the other process has it, so that a second copy on the
	// originating process has had at least as long to turn up as the first copy
	// took to cross Redis.
	expect(t, there, protocol.TypeChatMessage)
	expectNothing(t, here, protocol.TypeChatMessage, 500*time.Millisecond)
}

// Organisation isolation has to hold across Redis too. Everything published
// goes to every process, so a receiving hub that delivered by target alone
// would hand one tenant's presence and traffic to another's connections.
func TestAnOrganizationBroadcastDoesNotCrossOrganizationsThroughRedis(t *testing.T) {
	a, b := twoProcesses(t)

	ours := join(t, b, "conn-ours", "user-ours", "org-a")
	theirs := join(t, b, "conn-theirs", "user-theirs", "org-b")
	drain(ours)
	drain(theirs)

	a.BroadcastToOrg("org-a", protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "ours"}), "")

	expect(t, ours, protocol.TypeChatMessage)
	expectNothing(t, theirs, protocol.TypeChatMessage, 500*time.Millisecond)
}

// The same, for the presence path, which is the one that leaked across
// organisations in process before it was fixed.
func TestPresenceDoesNotCrossOrganizationsThroughRedis(t *testing.T) {
	a, b := twoProcesses(t)

	theirs := join(t, b, "conn-theirs", "user-theirs", "org-b")
	drain(theirs)

	join(t, a, "conn-ours", "user-ours", "org-a")

	expectNothing(t, theirs, protocol.TypePresenceChanged, 500*time.Millisecond)
}

// A channel message must not reach somebody who is not in the channel, however
// it arrived at their process.
func TestAChannelMessageFromAnotherProcessStillRespectsMembership(t *testing.T) {
	a, b := twoProcesses(t)
	roster := stubRoster{members: []string{"user-a", "user-b"}}
	a.SetChannelRoster(roster)
	b.SetChannelRoster(roster)

	member := join(t, b, "conn-member", "user-b", "org-1")
	outsider := join(t, b, "conn-outsider", "user-c", "org-2")
	join(t, a, "conn-sender", "user-a", "org-1")
	drain(member)
	drain(outsider)

	a.BroadcastToChannel("channel-1",
		protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "hello"}), "user-a")

	expect(t, member, protocol.TypeChatMessage)
	expectNothing(t, outsider, protocol.TypeChatMessage, 500*time.Millisecond)
}

// redisProxy is a TCP hop in front of Redis that a test can cut, which is the
// only way to produce the failure that matters here: the process stays up, its
// clients stay connected, and its subscription dies underneath it.
type redisProxy struct {
	listener net.Listener

	mu    sync.Mutex
	conns []net.Conn
}

func newRedisProxy(t *testing.T, upstream string) *redisProxy {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	proxy := &redisProxy{listener: listener}
	t.Cleanup(func() {
		_ = listener.Close()
		proxy.cut()
	})

	go proxy.serve(upstream)
	return proxy
}

func (p *redisProxy) serve(upstream string) {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", upstream)
		if err != nil {
			_ = client.Close()
			continue
		}

		p.mu.Lock()
		p.conns = append(p.conns, client, server)
		p.mu.Unlock()

		go func() {
			_, _ = io.Copy(server, client)
			_ = server.Close()
		}()
		go func() {
			_, _ = io.Copy(client, server)
			_ = client.Close()
		}()
	}
}

// url is where a client should point to reach Redis through the proxy.
func (p *redisProxy) url() string {
	return "redis://" + p.listener.Addr().String()
}

// cut drops every connection currently going through the proxy, the way a
// Redis failover or a restarted network path would.
func (p *redisProxy) cut() {
	p.mu.Lock()
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()

	for _, conn := range conns {
		_ = conn.Close()
	}
}

// upstreamAddr is the host:port of the Redis under test.
func upstreamAddr(t *testing.T) string {
	t.Helper()

	opts, err := redis.ParseURL(redisURL())
	if err != nil {
		t.Fatalf("parse %s: %v", EnvRedisURL, err)
	}
	return opts.Addr
}

// A subscription that dies has to come back by itself. If it does not, the
// process keeps serving its clients and quietly stops hearing from the rest of
// the cluster — which is issue #42 again, arrived at from the other direction
// and with no restart to end it.
//
// The sessions open at the time are ended: pub/sub does not replay, so there is
// no way to know what was published during the gap, and a client that stays
// connected would never learn it had missed anything.
func TestASubscriberThatLosesRedisResubscribes(t *testing.T) {
	proxy := newRedisProxy(t, upstreamAddr(t))
	channel := "teamup:test:fanout:" + uuid.NewString()

	publisher := processOn(t, testRedis(t), channel)
	subscriber := processOn(t, testRedisAt(t, proxy.url()), channel)

	before := join(t, subscriber, "conn-before", "user-1", "org-1")
	drain(before)
	publisher.BroadcastToUser("user-1", protocol.NewMessage(protocol.TypeCallInvited, map[string]string{"callId": "a"}))
	expect(t, before, protocol.TypeCallInvited)

	proxy.cut()

	select {
	case <-before.ctx.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("the sessions were left open after a fan-out gap, believing they were up to date")
	}

	// And the process is receiving again. Whatever was published during the gap
	// is gone — that is the accepted trade — so keep offering until one lands.
	after := join(t, subscriber, "conn-after", "user-2", "org-1")
	drain(after)

	deadline := time.Now().Add(30 * time.Second)
	for {
		publisher.BroadcastToUser("user-2", protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "back?"}))
		select {
		case msg := <-after.SendCh:
			if msg.Type == protocol.TypeChatMessage {
				return
			}
		case <-time.After(250 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the subscriber never resubscribed: this process is deaf to the rest of the cluster")
		}
	}
}

// blockingRoster holds delivery up, standing in for a slow database.
type blockingRoster struct{ release chan struct{} }

func (r blockingRoster) ChannelMemberIDs(ctx context.Context, _ string) ([]string, error) {
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return nil, nil
}

// A subscriber that cannot deliver as fast as it receives must not turn into an
// unbounded queue in memory. It drops, counts and says so — the same answer the
// per-connection send buffer already gives, for the same reason.
func TestASlowSubscriberDropsRatherThanGrowing(t *testing.T) {
	channel := "teamup:test:fanout:" + uuid.NewString()
	publisher := processOn(t, testRedis(t), channel)
	subscriber := processOn(t, testRedis(t), channel)

	// Delivery on the receiving process wedges on the first envelope, so
	// everything behind it piles up in the inbound queue.
	roster := blockingRoster{release: make(chan struct{})}
	subscriber.SetChannelRoster(roster)
	defer close(roster.release)

	before := testutil.ToFloat64(wsFanoutDropped.WithLabelValues("inbound_full"))

	// Well past the depth of the queue, so that a bounded one has to say so.
	msg := protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "hello"})
	for i := 0; i < 8*fanoutQueue; i++ {
		publisher.BroadcastToChannel("channel-1", msg, "")
	}

	// Asserted after the burst rather than during it: the receiving process is
	// several hops behind the sending one, and an unbounded queue would still
	// be filling quietly at the moment the last one went out.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if testutil.ToFloat64(wsFanoutDropped.WithLabelValues("inbound_full")) > before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d envelopes went into a subscriber that is delivering none and nothing was dropped: the queue is unbounded", 8*fanoutQueue)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// blackHole accepts connections and then says nothing at all, which is what a
// Redis that has stopped answering looks like from inside the process.
func blackHole(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range held {
			_ = conn.Close()
		}
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()

	return "redis://" + listener.Addr().String()
}

// Broadcasting runs on the goroutine handling somebody's request. A Redis that
// has stopped answering has to cost a counted drop, not a chat that stops
// working for the people on this replica — who can still be served perfectly
// well without it.
func TestABroadcastDoesNotWaitForARedisThatIsNotAnswering(t *testing.T) {
	opts, err := redis.ParseURL(blackHole(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	fanout := NewFanout(client, zerolog.Nop())
	fanout.channel = "teamup:test:fanout:" + uuid.NewString()
	hub.SetFanout(fanout)
	fanout.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = fanout.Close(ctx)
	})

	here := join(t, hub, "conn-here", "user-1", "org-1")
	drain(here)

	before := testutil.ToFloat64(wsFanoutDropped.WithLabelValues("outbound_full"))

	msg := protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "hello"})
	started := time.Now()
	for i := 0; i < 4*fanoutQueue; i++ {
		hub.BroadcastToUser("user-1", msg)
	}
	elapsed := time.Since(started)

	// One publish alone is allowed fanoutPublishTimeout to fail in. Callers
	// waiting on that, thousands of times over, is the failure being ruled out.
	if elapsed > fanoutPublishTimeout {
		t.Errorf("broadcasting waited on Redis: %d broadcasts took %s", 4*fanoutQueue, elapsed)
	}
	if dropped := testutil.ToFloat64(wsFanoutDropped.WithLabelValues("outbound_full")) - before; dropped == 0 {
		t.Error("nothing was counted as dropped, so the outbound queue grew without bound instead")
	}

	// Local delivery is unaffected: these people are reachable and Redis has
	// nothing to do with reaching them.
	expect(t, here, protocol.TypeChatMessage)
}

// Anything at all may publish onto a Redis channel — another tenant of the same
// Redis, a half-upgraded replica, somebody with redis-cli. A publication this
// build cannot make sense of has to be counted and ignored. Delivering one
// dereferences a nil message on a goroutine with no recover, and that is not a
// dropped message, it is the process.
func TestAMalformedPublicationDoesNotKillTheProcess(t *testing.T) {
	channel := "teamup:test:fanout:" + uuid.NewString()
	client := testRedis(t)
	hub := processOn(t, testRedis(t), channel)

	conn := join(t, hub, "conn-1", "user-1", "org-1")
	drain(conn)

	ctx := context.Background()
	for _, bad := range []string{
		`{"origin":"other","kind":"user","target":"user-1"}`,
		`{"origin":"other","kind":"user","target":"user-1","msg":null}`,
		`not json at all`,
		`{"origin":"other","kind":"a-kind-from-a-later-version","target":"user-1","msg":{"type":"chat.message"}}`,
	} {
		if err := client.Publish(ctx, channel, bad).Err(); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// A good one behind them still lands, which is what says the subscriber
	// survived rather than merely not having got there yet.
	if err := client.Publish(ctx, channel,
		`{"origin":"other","kind":"user","target":"user-1","msg":{"type":"chat.message","payload":{}}}`).Err(); err != nil {
		t.Fatalf("publish: %v", err)
	}

	expect(t, conn, protocol.TypeChatMessage)
}
