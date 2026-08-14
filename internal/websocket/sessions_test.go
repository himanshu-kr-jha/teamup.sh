package websocket

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/teamup/teamup/pkg/protocol"
)

// clusterPresence is one presence store shared by several hubs in a test
// binary, which is what the Redis-backed one is to several processes. It
// records what it was told so a test can say whether somebody was published as
// offline, not merely forgotten by one hub.
//
// The Redis implementation of the same interface, and the semantics asserted
// here, are covered against a real Redis in internal/presence.
type clusterPresence struct {
	mu       sync.Mutex
	sessions map[string]map[string]bool // userID -> sessionID
	offline  []string
	online   []string

	// arriving, when set, holds each RemoveSession until the expected number
	// of them are inside it at once. Reaching the registry is a network round
	// trip in the real implementation, and a drain sends every session into it
	// together; an in-memory map is far too quick to produce that by itself.
	arriving *barrier
}

// barrier releases its waiters once the last of them has arrived.
type barrier struct {
	mu      sync.Mutex
	waiting int
	want    int
	open    chan struct{}
}

func newBarrier(want int) *barrier {
	return &barrier{want: want, open: make(chan struct{})}
}

func (b *barrier) arrive() {
	b.mu.Lock()
	b.waiting++
	last := b.waiting == b.want
	b.mu.Unlock()

	if last {
		close(b.open)
	}
	<-b.open
}

func newClusterPresence() *clusterPresence {
	return &clusterPresence{sessions: map[string]map[string]bool{}}
}

func (p *clusterPresence) SetOnline(userID, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.online = append(p.online, userID)
	return nil
}

func (p *clusterPresence) SetOffline(userID, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offline = append(p.offline, userID)
	return nil
}

func (p *clusterPresence) UpdateStatus(string, string, string, string, string) error { return nil }
func (p *clusterPresence) HandlePresenceUpdate(context.Context, WSConnection, *protocol.Message) {
}
func (p *clusterPresence) GetPresence(string, string) (*Presence, error) { return nil, nil }
func (p *clusterPresence) GetAllOnline(string) ([]Presence, error)       { return nil, nil }

func (p *clusterPresence) AddSession(userID, sessionID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions[userID] == nil {
		p.sessions[userID] = map[string]bool{}
	}
	p.sessions[userID][sessionID] = true
	return nil
}

func (p *clusterPresence) TouchSession(userID, sessionID string) error {
	return p.AddSession(userID, sessionID)
}

func (p *clusterPresence) RemoveSession(userID, sessionID string) (int, error) {
	if p.arriving != nil {
		p.arriving.arrive()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions[userID], sessionID)
	return len(p.sessions[userID]), nil
}

func (p *clusterPresence) AnySession(userID string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sessions[userID]) > 0, nil
}

func (p *clusterPresence) wasMarkedOffline(userID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.offline {
		if id == userID {
			return true
		}
	}
	return false
}

// twoHubsSharing returns two hubs backed by one presence store, which is two
// replicas of the server.
func twoHubsSharing(pres *clusterPresence) (*Hub, *Hub) {
	return NewHub(nil, pres, nil, zerolog.Nop()), NewHub(nil, pres, nil, zerolog.Nop())
}

// Closing a tab served by one replica used to publish the person as offline,
// because "their last connection" was asked of that process's own map. Their
// phone, connected to another replica, was still there — and their colleagues
// were told they had left, with nothing to correct it until they happened to
// reconnect.
func TestSomebodyIsNotOfflineWhileTheyHaveASocketOnAnotherProcess(t *testing.T) {
	pres := newClusterPresence()
	a, b := twoHubsSharing(pres)

	laptop := testConnection(a, "conn-laptop", "user-1", "org-1")
	phone := testConnection(b, "conn-phone", "user-1", "org-1")
	a.Register(laptop)
	b.Register(phone)

	a.Unregister(laptop)

	if pres.wasMarkedOffline("user-1") {
		t.Error("they were published as offline while still connected to another replica")
	}
	if !b.IsConnected("user-1") {
		t.Error("the replica still holding their socket stopped seeing them")
	}
}

// The other half of it: once the last socket anywhere has gone, they really
// have gone, and somebody has to say so.
func TestTheLastSocketAnywhereIsWhatMarksThemOffline(t *testing.T) {
	pres := newClusterPresence()
	a, b := twoHubsSharing(pres)

	laptop := testConnection(a, "conn-laptop", "user-1", "org-1")
	phone := testConnection(b, "conn-phone", "user-1", "org-1")
	a.Register(laptop)
	b.Register(phone)

	a.Unregister(laptop)
	b.Unregister(phone)

	if !pres.wasMarkedOffline("user-1") {
		t.Error("their last socket closed and nobody was told they had gone")
	}
}

// Whether somebody is reachable decides whether a web push is sent and whether
// a caller is told anybody is there. Answered per replica it is wrong for
// everybody whose socket is somewhere else.
func TestReachabilityIsAskedOfTheWholeCluster(t *testing.T) {
	pres := newClusterPresence()
	a, b := twoHubsSharing(pres)

	if a.IsConnected("user-1") {
		t.Error("nobody has connected anywhere yet")
	}

	phone := testConnection(b, "conn-phone", "user-1", "org-1")
	b.Register(phone)

	if !a.IsConnected("user-1") {
		t.Error("a replica with none of their sockets reported them unreachable, so their phone never rings and they get a duplicate push")
	}

	b.Unregister(phone)

	if a.IsConnected("user-1") {
		t.Error("their last socket has gone and they are still reported as reachable")
	}
}

// Every session of one person ending at once is what a shutdown is: the drain
// cancels them all and each unregisters on its own goroutine.
//
// Deciding who announces the departure from "was that the last one in my map"
// does not survive that. Each goroutine empties the map, then gives up its
// claim — so the one that saw the map empty can reach the registry before the
// others have let go, find their claims still there, and stay quiet. The
// others have already decided it is not their business. Nobody says anything,
// and the whole company is left looking at a green dot for somebody whose
// server has gone.
func TestEverySessionClosingAtOnceStillMarksThemOffline(t *testing.T) {
	pres := newClusterPresence()
	hub, _ := twoHubsSharing(pres)

	const sessions = 3
	pres.arriving = newBarrier(sessions)
	conns := make([]*Connection, 0, sessions)
	for i := 0; i < sessions; i++ {
		conn := testConnection(hub, fmt.Sprintf("conn-%d", i), "user-1", "org-1")
		hub.Register(conn)
		conns = append(conns, conn)
	}

	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Add(1)
		go func(conn *Connection) {
			defer wg.Done()
			hub.Unregister(conn)
		}(conn)
	}
	wg.Wait()

	if !pres.wasMarkedOffline("user-1") {
		t.Error("every session ended and nobody published them as offline")
	}
}

// Two sockets of the same person on the same replica must still be counted
// separately across the cluster, or closing one tab takes the other with it.
func TestTwoTabsOnOneReplicaAreTwoSessions(t *testing.T) {
	pres := newClusterPresence()
	a, _ := twoHubsSharing(pres)

	first := testConnection(a, "conn-1", "user-1", "org-1")
	second := testConnection(a, "conn-2", "user-1", "org-1")
	a.Register(first)
	a.Register(second)

	a.Unregister(first)

	if pres.wasMarkedOffline("user-1") {
		t.Error("closing one tab marked them offline while the other was open")
	}
	if !a.IsConnected("user-1") {
		t.Error("their other tab stopped counting")
	}
}
