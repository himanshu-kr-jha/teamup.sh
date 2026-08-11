package websocket

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"

	"github.com/teamup/teamup/pkg/protocol"
)

// stubPresence records the calls the hub makes without touching Redis.
type stubPresence struct{}

func (stubPresence) SetOnline(string, string) error                                        { return nil }
func (stubPresence) SetOffline(string, string) error                                       { return nil }
func (stubPresence) UpdateStatus(string, string, string, string, string) error             { return nil }
func (stubPresence) HandlePresenceUpdate(context.Context, WSConnection, *protocol.Message) {}
func (stubPresence) GetPresence(string, string) (*Presence, error)                         { return nil, nil }
func (stubPresence) GetAllOnline(string) ([]Presence, error)                               { return nil, nil }

func testConnection(hub *Hub, id, userID, orgID string) *Connection {
	ctx, cancel := context.WithCancel(context.Background())
	return &Connection{
		ID:            id,
		userID:        userID,
		orgID:         orgID,
		SendCh:        make(chan *protocol.Message, 8),
		Subscriptions: map[string]bool{},
		ctx:           ctx,
		cancel:        cancel,
		hub:           hub,
	}
}

// Register used to announce presence while still holding the write lock.
// sync.RWMutex is not reentrant, so the read lock inside broadcastPresence
// deadlocked the hub on the very first connection — and because no browser
// could open a socket at all, nobody noticed until one did.
func TestRegisteringAConnectionDoesNotDeadlockTheHub(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	conn := testConnection(hub, "conn-1", "user-1", "org-1")

	done := make(chan struct{})
	go func() {
		hub.Register(conn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Register deadlocked")
	}
}

func TestUnregisteringAConnectionDoesNotDeadlockTheHub(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	conn := testConnection(hub, "conn-1", "user-1", "org-1")
	hub.Register(conn)

	done := make(chan struct{})
	go func() {
		hub.Unregister(conn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Unregister deadlocked")
	}
}

// The symptom that surfaced the deadlock: once the hub was stuck, anything
// that needed the lock hung for ever, so creating a call never returned.
func TestTheHubStillDeliversAfterSomebodyConnects(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	conn := testConnection(hub, "conn-1", "user-1", "org-1")
	hub.Register(conn)

	// Drain the presence announcement Register queued to this same connection.
	drain(conn)

	done := make(chan struct{})
	go func() {
		hub.BroadcastToUser("user-1", protocol.NewMessage(protocol.TypeCallInvited, map[string]string{"callId": "abc"}))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("BroadcastToUser blocked — the hub is wedged")
	}

	select {
	case msg := <-conn.SendCh:
		if msg.Type != protocol.TypeCallInvited {
			t.Errorf("wrong message delivered: %s", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("the message never reached the connection")
	}
}

func drain(conn *Connection) {
	for {
		select {
		case <-conn.SendCh:
		default:
			return
		}
	}
}

// Presence used to be announced to every connected client on the server,
// whichever organization they belonged to.
func TestPresenceStaysInsideTheOrganization(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())

	mine := testConnection(hub, "conn-mine", "user-mine", "org-a")
	theirs := testConnection(hub, "conn-theirs", "user-theirs", "org-b")
	hub.Register(mine)
	hub.Register(theirs)
	drain(mine)
	drain(theirs)

	hub.broadcastPresence("org-a", "user-mine", "online", "")

	select {
	case msg := <-mine.SendCh:
		if msg.Type != protocol.TypePresenceChanged {
			t.Errorf("wrong message: %s", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("the organization's own member was not told")
	}

	select {
	case msg := <-theirs.SendCh:
		t.Errorf("another organization was told about our presence: %s", msg.Type)
	default:
	}
}

// Registering a second organization's connection must not announce it to the
// first, which is the path that leaked in practice.
func TestConnectingDoesNotAnnounceAcrossOrganizations(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())

	mine := testConnection(hub, "conn-mine", "user-mine", "org-a")
	hub.Register(mine)
	drain(mine)

	theirs := testConnection(hub, "conn-theirs", "user-theirs", "org-b")
	hub.Register(theirs)

	select {
	case msg := <-mine.SendCh:
		t.Errorf("we were told somebody in another organization came online: %s", msg.Type)
	case <-time.After(200 * time.Millisecond):
	}
}

// stubRoster answers with a fixed set of members.
type stubRoster struct{ members []string }

func (r stubRoster) ChannelMemberIDs(context.Context, string) ([]string, error) {
	return r.members, nil
}

// Channel delivery went through a subscription table that nothing ever filled
// in, so a message reached nobody and the only way to see one was to reload.
func TestAChannelMessageReachesItsMembers(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	hub.SetChannelRoster(stubRoster{members: []string{"user-a", "user-b"}})

	a := testConnection(hub, "conn-a", "user-a", "org-1")
	b := testConnection(hub, "conn-b", "user-b", "org-1")
	outsider := testConnection(hub, "conn-c", "user-c", "org-1")
	for _, c := range []*Connection{a, b, outsider} {
		hub.Register(c)
	}
	// Drained after every registration: each one announces presence to the
	// connections already there.
	for _, c := range []*Connection{a, b, outsider} {
		drain(c)
	}

	hub.BroadcastToChannel("channel-1",
		protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "hello"}), "user-a")

	// The sender is excluded; they already have it optimistically.
	select {
	case msg := <-a.SendCh:
		t.Errorf("the sender got their own message back: %s", msg.Type)
	default:
	}

	select {
	case msg := <-b.SendCh:
		if msg.Type != protocol.TypeChatMessage {
			t.Errorf("wrong message: %s", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("a member of the channel never received the message")
	}

	select {
	case msg := <-outsider.SendCh:
		t.Errorf("somebody outside the channel received it: %s", msg.Type)
	default:
	}
}

// Somebody who joins a channel while connected must start receiving straight
// away, without reconnecting — which a subscription captured at connect time
// could never do.
func TestDeliveryFollowsMembershipWithoutReconnecting(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	roster := &mutableRoster{}
	hub.SetChannelRoster(roster)

	conn := testConnection(hub, "conn-late", "user-late", "org-1")
	hub.Register(conn)
	drain(conn)

	// Not a member yet.
	hub.BroadcastToChannel("channel-1", protocol.NewMessage(protocol.TypeChatMessage, nil), "")
	select {
	case <-conn.SendCh:
		t.Fatal("received a message for a channel they are not in")
	default:
	}

	roster.set([]string{"user-late"})
	hub.BroadcastToChannel("channel-1", protocol.NewMessage(protocol.TypeChatMessage, nil), "")
	select {
	case <-conn.SendCh:
	case <-time.After(time.Second):
		t.Error("joining a channel did not start delivery")
	}
}

type mutableRoster struct {
	mu      sync.Mutex
	members []string
}

func (r *mutableRoster) set(members []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.members = members
}

func (r *mutableRoster) ChannelMemberIDs(context.Context, string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.members...), nil
}

// BroadcastToUser used to take the read lock only long enough to fetch the
// user's inner connection map, release it, and then range over that very map.
// A concurrent Register or Unregister writing the same map makes that
// `fatal error: concurrent map iteration and map write` — a runtime throw
// gin.Recovery cannot catch, so the whole process dies. The trigger is
// ordinary: ringing somebody whose socket is opening or closing, which is what
// a page refresh during a call looks like.
func TestBroadcastingToSomebodyWhoIsReconnecting(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())

	const userID = "user-1"
	// One connection stays, so there is always a map to iterate.
	hub.Register(testConnection(hub, "conn-resident", userID, "org-1"))

	msg := protocol.NewMessage(protocol.TypeCallInvited, map[string]string{"callId": "abc"})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// A page refresh: the same person's other sockets opening and closing.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			conn := testConnection(hub, fmt.Sprintf("conn-%d", i), userID, "org-1")
			hub.Register(conn)
			hub.Unregister(conn)
		}
	}()

	// Somebody placing a call to them, which is how this was first hit.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 5000; i++ {
			hub.BroadcastToUser(userID, msg)
		}
	}()

	wg.Wait()
}

// The whole organization is broadcast to on the same maps. This passed before
// BroadcastToUser was fixed — BroadcastToOrg held the read lock across its
// whole body — so it is the audit of the other broadcast paths written down,
// not a reproduction.
func TestBroadcastingToAnOrganizationWhilePeopleReconnect(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	hub.Register(testConnection(hub, "conn-resident", "user-resident", "org-1"))

	msg := protocol.NewMessage(protocol.TypeChatMessage, map[string]string{"content": "hello"})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			conn := testConnection(hub, fmt.Sprintf("conn-%d", i), fmt.Sprintf("user-%d", i), "org-1")
			hub.Register(conn)
			hub.Unregister(conn)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 5000; i++ {
			hub.BroadcastToOrg("org-1", msg, "")
		}
	}()

	wg.Wait()
}

// sockets hands out connections with a real WebSocket behind them, so a test
// can run the actual ReadLoop and its cleanup rather than a copy of it.
type sockets struct {
	server *httptest.Server
	accept chan *websocket.Conn
}

func newSockets(t *testing.T) *sockets {
	t.Helper()

	s := &sockets{accept: make(chan *websocket.Conn, 1)}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s.accept <- conn
	}))
	t.Cleanup(s.server.Close)
	return s
}

// open returns a hub connection and the client end of its socket.
func (s *sockets) open(t *testing.T, hub *Hub, id, userID, orgID string) (*Connection, *websocket.Conn) {
	t.Helper()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDial()
	client, _, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(s.server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return &Connection{
		ID:            id,
		userID:        userID,
		orgID:         orgID,
		WS:            <-s.accept,
		SendCh:        make(chan *protocol.Message, 8),
		Subscriptions: map[string]bool{},
		ctx:           ctx,
		cancel:        cancel,
		hub:           hub,
		logger:        zerolog.Nop(),
	}, client
}

// ReadLoop's cleanup used to close SendCh, but every broadcast path enqueues to
// connection pointers it took from the hub maps before releasing the lock, so
// nothing orders "this connection is finished" against "somebody is sending to
// it". A select with a default arm does not make that safe: `send on closed
// channel` panics whatever the default says, and the nearest producers —
// Ring, DeliverPendingInvitations, the chat handlers — run in goroutines with
// no recover, so the panic takes the process down.
func TestBroadcastingToAConnectionThatIsClosing(t *testing.T) {
	pool := newSockets(t)
	msg := protocol.NewMessage(protocol.TypeCallInvited, map[string]string{"callId": "abc"})

	for round := 0; round < 50; round++ {
		hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
		conn, client := pool.open(t, hub, fmt.Sprintf("conn-%d", round), "user-1", "org-1")
		hub.Register(conn)
		go conn.ReadLoop()

		stop := make(chan struct{})
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				hub.BroadcastToUser("user-1", msg)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(stop)
			// Closing the far end is what ends a session in practice: a tab
			// closing, a refresh, a phone changing network.
			_ = client.Close(websocket.StatusNormalClosure, "")
			time.Sleep(2 * time.Millisecond)
		}()

		wg.Wait()
		hub.Unregister(conn)
	}
}

// stubDeliverer records who the hub tried to ring when they connected.
type stubDeliverer struct {
	mu      sync.Mutex
	rung    []string
	arrived chan struct{}
}

func (s *stubDeliverer) DeliverPendingInvitations(_ context.Context, userID uuid.UUID) {
	s.mu.Lock()
	s.rung = append(s.rung, userID.String())
	s.mu.Unlock()
	select {
	case s.arrived <- struct{}{}:
	default:
	}
}

// Being connected is what separates a phone that rings from one that is merely
// being called, so the hub has to be able to say which.
func TestTheHubKnowsWhoIsConnected(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	conn := testConnection(hub, "conn-1", "user-1", "org-1")

	if hub.IsConnected("user-1") {
		t.Error("nobody has connected yet")
	}
	hub.Register(conn)
	if !hub.IsConnected("user-1") {
		t.Error("somebody with a session open is reachable")
	}
	hub.Unregister(conn)
	if hub.IsConnected("user-1") {
		t.Error("their session is gone, so they are not reachable")
	}
}

// Somebody who was away while being called should have their phone ring when
// they come back, which means connecting is what triggers the attempt.
func TestConnectingRingsForCallsThatWereWaiting(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	deliverer := &stubDeliverer{arrived: make(chan struct{}, 1)}
	hub.SetCallInviteDeliverer(deliverer)

	userID := uuid.New()
	hub.Register(testConnection(hub, "conn-1", userID.String(), "org-1"))

	select {
	case <-deliverer.arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("connecting should look for calls still waiting on them")
	}

	deliverer.mu.Lock()
	defer deliverer.mu.Unlock()
	if len(deliverer.rung) != 1 || deliverer.rung[0] != userID.String() {
		t.Errorf("the wrong person was rung: %v", deliverer.rung)
	}
}
