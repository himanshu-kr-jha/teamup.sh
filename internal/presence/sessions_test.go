package presence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/teamup/teamup/internal/websocket"
)

// The hub asks presence to count somebody's sockets across the whole cluster.
// Nothing enforces that from the hub's side, so it is asserted here.
var _ websocket.PresenceSessions = (*Service)(nil)

// sessionsFor returns a user nobody else in the suite is using, and clears
// their registry entry afterwards.
func sessionsFor(t *testing.T, client *redis.Client) string {
	t.Helper()

	userID := uuid.NewString()
	t.Cleanup(func() {
		client.Del(context.Background(), sessionsKey(userID))
	})
	return userID
}

// The question "was that their last socket" is a question about every process,
// not about the one that happens to be handling the disconnect. Answered
// locally, somebody closing a tab on one replica is published as offline while
// their phone is still connected to another, and their colleagues watch them
// disappear.
func TestASocketOnAnotherProcessStillCounts(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	userID := sessionsFor(t, client)

	if err := svc.AddSession(userID, "node-a:conn-1"); err != nil {
		t.Fatalf("add on the first process: %v", err)
	}
	if err := svc.AddSession(userID, "node-b:conn-1"); err != nil {
		t.Fatalf("add on the second process: %v", err)
	}

	remaining, err := svc.RemoveSession(userID, "node-a:conn-1")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if remaining != 1 {
		t.Errorf("sockets left after one process let go: got %d want 1", remaining)
	}

	connected, err := svc.AnySession(userID)
	if err != nil {
		t.Fatalf("any: %v", err)
	}
	if !connected {
		t.Error("they are still connected to the other process and were reported as gone")
	}

	remaining, err = svc.RemoveSession(userID, "node-b:conn-1")
	if err != nil {
		t.Fatalf("remove the last: %v", err)
	}
	if remaining != 0 {
		t.Errorf("sockets left after the last one went: got %d want 0", remaining)
	}
}

// Nobody is connected until somebody says so, and a user with no entry at all
// must not read as reachable.
func TestSomebodyWithNoSessionsIsNotConnected(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	userID := sessionsFor(t, client)

	connected, err := svc.AnySession(userID)
	if err != nil {
		t.Fatalf("any: %v", err)
	}
	if connected {
		t.Error("somebody who has never connected was reported as connected")
	}
}

// A process that is killed, OOMs or is rescheduled never gets to let go of its
// sessions. Nothing else is allowed to remove them — they belong to that
// process — so they have to stop counting by themselves, or one crash leaves
// people permanently "connected" to a replica that no longer exists and their
// colleagues are never told they left.
func TestTheSessionsOfAProcessThatDiedStopCountingByThemselves(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	svc.ttl = 300 * time.Millisecond
	userID := sessionsFor(t, client)

	if err := svc.AddSession(userID, "node-dead:conn-1"); err != nil {
		t.Fatalf("add: %v", err)
	}

	connected, err := svc.AnySession(userID)
	if err != nil {
		t.Fatalf("any: %v", err)
	}
	if !connected {
		t.Fatal("a session that was just claimed does not count")
	}

	time.Sleep(500 * time.Millisecond)

	connected, err = svc.AnySession(userID)
	if err != nil {
		t.Fatalf("any after the deadline: %v", err)
	}
	if connected {
		t.Error("the sessions of a process that stopped refreshing them still count")
	}
}

// The price of that deadline is that a process which is alive has to keep
// saying so, or the people it is serving blink out from under it.
func TestARunningProcessKeepsItsSessionsAlive(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	svc.ttl = 400 * time.Millisecond
	userID := sessionsFor(t, client)

	if err := svc.AddSession(userID, "node-live:conn-1"); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Three refreshes across more than two deadlines' worth of time.
	for i := 0; i < 6; i++ {
		time.Sleep(150 * time.Millisecond)
		if err := svc.TouchSession(userID, "node-live:conn-1"); err != nil {
			t.Fatalf("touch: %v", err)
		}
	}

	connected, err := svc.AnySession(userID)
	if err != nil {
		t.Fatalf("any: %v", err)
	}
	if !connected {
		t.Error("somebody whose process is still refreshing their session was dropped")
	}
}

// One process letting go of its own session must not take another process's
// session for the same person with it.
func TestAProcessOnlyReleasesItsOwnSessions(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	userID := sessionsFor(t, client)

	for _, sessionID := range []string{"node-a:conn-1", "node-a:conn-2", "node-b:conn-1"} {
		if err := svc.AddSession(userID, sessionID); err != nil {
			t.Fatalf("add %s: %v", sessionID, err)
		}
	}

	remaining, err := svc.RemoveSession(userID, "node-a:conn-1")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if remaining != 2 {
		t.Errorf("sockets left: got %d want 2 (the other tab here, and the other process)", remaining)
	}
}
