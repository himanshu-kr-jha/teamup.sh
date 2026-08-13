package websocket

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/teamup/teamup/pkg/protocol"
)

// recordingPresence remembers what the hub asked it to do, so a test can say
// whether somebody was actually marked offline rather than only whether the
// hub forgot about them.
type recordingPresence struct {
	mu       sync.Mutex
	offline  []string
	refresh  []string
	setCalls int
}

func (p *recordingPresence) SetOnline(userID, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setCalls++
	return nil
}

func (p *recordingPresence) SetOffline(userID, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offline = append(p.offline, userID)
	return nil
}

func (p *recordingPresence) Touch(userID, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refresh = append(p.refresh, userID)
	return nil
}

func (p *recordingPresence) UpdateStatus(string, string, string, string, string) error { return nil }
func (p *recordingPresence) HandlePresenceUpdate(context.Context, WSConnection, *protocol.Message) {
}
func (p *recordingPresence) GetPresence(string, string) (*Presence, error) { return nil, nil }
func (p *recordingPresence) GetAllOnline(string) ([]Presence, error)       { return nil, nil }

func (p *recordingPresence) wentOffline(userID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.offline {
		if id == userID {
			return true
		}
	}
	return false
}

func (p *recordingPresence) refreshed(userID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.refresh {
		if id == userID {
			return true
		}
	}
	return false
}

// cleanupStaleConnections was an empty loop body — it ranged over the
// connections, assigned each to _, and returned. These are application-level
// pings over an established TCP connection, so when a client disappears
// without a FIN — a suspended laptop, a NAT entry expiring, a phone changing
// network — writes keep succeeding into the socket buffer and nothing ever
// notices. The connection stayed registered, its goroutines and 256-slot
// buffer stayed resident, and because SetOffline only runs from Unregister the
// user was shown online to their whole organisation indefinitely.
func TestAConnectionThatStoppedAnsweringIsReaped(t *testing.T) {
	pres := &recordingPresence{}
	hub := NewHub(nil, pres, nil, zerolog.Nop())
	pool := newSockets(t)

	conn, _ := pool.open(t, hub, "conn-dead", "user-dead", "org-1")
	hub.Register(conn)

	// It answered when it connected, and then went quiet.
	conn.lastSeen.Store(time.Now().Add(-staleAfter - time.Second).UnixNano())

	hub.cleanupStaleConnections()

	if hub.IsConnected("user-dead") {
		t.Error("a connection that stopped answering is still registered")
	}
	if !pres.wentOffline("user-dead") {
		t.Error("the user is still shown online to their organisation")
	}
	select {
	case <-conn.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Error("the connection's goroutines were never stopped")
	}
}

// The reaper must not disconnect people who are simply quiet: not sending
// anything is normal, answering the ping is what counts.
func TestAConnectionThatIsStillAnsweringIsLeftAlone(t *testing.T) {
	pres := &recordingPresence{}
	hub := NewHub(nil, pres, nil, zerolog.Nop())
	pool := newSockets(t)

	conn, _ := pool.open(t, hub, "conn-live", "user-live", "org-1")
	hub.Register(conn)

	hub.cleanupStaleConnections()

	if !hub.IsConnected("user-live") {
		t.Error("a connection that is answering was reaped")
	}
	if pres.wentOffline("user-live") {
		t.Error("a user who is here was marked offline")
	}
}

// The reaper only has something to compare against if inbound frames are
// recorded. conn.pong was declared in the protocol and had no case in
// handleMessage, so a client answering the ping was logged as an unknown
// message type and nothing was remembered about it.
func TestAnsweringThePingKeepsAConnectionAlive(t *testing.T) {
	pres := &recordingPresence{}
	hub := NewHub(nil, pres, nil, zerolog.Nop())
	pool := newSockets(t)

	conn, client := pool.open(t, hub, "conn-answers", "user-answers", "org-1")
	hub.Register(conn)
	go conn.ReadLoop()

	// Long enough ago that the reaper would take it.
	stale := time.Now().Add(-staleAfter - time.Second).UnixNano()
	conn.lastSeen.Store(stale)

	writeFrame(t, client, `{"type":"conn.pong","payload":{}}`)

	deadline := time.Now().Add(2 * time.Second)
	for conn.lastSeen.Load() == stale && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	hub.cleanupStaleConnections()

	if !hub.IsConnected("user-answers") {
		t.Error("a client that answered the ping was reaped anyway")
	}
}

// The presence entries carry a TTL so that a process which is killed or OOMs
// does not leave everybody it served online for ever. That only works if
// something refreshes the TTL for people who are still here, and the ping the
// writer already sends every thirty seconds is exactly that moment.
func TestTheHeartbeatRefreshesPresence(t *testing.T) {
	pres := &recordingPresence{}
	hub := NewHub(nil, pres, nil, zerolog.Nop())
	pool := newSockets(t)

	conn, client := pool.open(t, hub, "conn-1", "user-1", "org-1")

	if err := conn.heartbeat(); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	if msg := readFrame(t, client); msg.Type != protocol.TypeConnPing {
		t.Errorf("the client was not pinged: got %q", msg.Type)
	}
	if !pres.refreshed("user-1") {
		t.Error("the ping went out but the presence entry was not refreshed, so its TTL expires under somebody who is still here")
	}
}
