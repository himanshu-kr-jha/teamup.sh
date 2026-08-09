package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

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
