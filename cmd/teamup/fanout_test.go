package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"nhooyr.io/websocket"
)

// Two replicas, for real: two server processes against one database and one
// Redis, with a socket on each.
//
// This is the deployment the Helm chart used to refuse to render. Everything
// below it is unit-tested — the fan-out in internal/websocket, the session
// registry in internal/presence — but nothing except starting two processes
// proves that main.go wires them together, and that is the part a rolling
// deploy depends on.

// wsFrame is one message read off a session.
type wsFrame struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// openSession dials the server's WebSocket and returns a stream of frames.
func openSession(t *testing.T, srv *testServer, token string) (*websocket.Conn, <-chan wsFrame) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, srv.baseURL+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{"at=" + token}},
	})
	if err != nil {
		t.Fatalf("open websocket: %v\n%s", err, srv.logs.String())
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	frames := make(chan wsFrame, 64)
	go func() {
		defer close(frames)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var frame wsFrame
			if json.Unmarshal(data, &frame) != nil {
				continue
			}
			select {
			case frames <- frame:
			default:
			}
		}
	}()
	return conn, frames
}

// awaitFrame waits for a frame of one type, ignoring the rest.
func awaitFrame(t *testing.T, frames <-chan wsFrame, msgType string, within time.Duration) wsFrame {
	t.Helper()

	deadline := time.After(within)
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("the session closed before %s arrived", msgType)
			}
			if frame.Type == msgType {
				return frame
			}
		case <-deadline:
			t.Fatalf("no %s within %s", msgType, within)
		}
	}
}

// settle drains whatever a freshly opened session was told about itself.
func settle(frames <-chan wsFrame, for_ time.Duration) {
	deadline := time.After(for_)
	for {
		select {
		case <-frames:
		case <-deadline:
			return
		}
	}
}

// twoReplicas starts two servers against the same database and Redis. Only the
// first provisions the organisation; the second finds it already there, which
// is what the second pod of a Deployment does.
func twoReplicas(t *testing.T) (*testServer, *testServer) {
	t.Helper()

	first := startServer(t, serverOptions{autoSetup: true})
	second := startServer(t, serverOptions{})
	return first, second
}

// forgetPresence clears what this test leaves behind for the shared harness
// user.
//
// The harness kills its servers rather than draining them, so any session still
// open at the end is exactly the case the session registry is built to survive:
// the claim sits in Redis until the deadline its owner last set, and until then
// the person is correctly considered still connected somewhere. Correct in
// production, and lethal to the next test in the package — every test here logs
// in as the same admin, so a claim left by one becomes a phantom device in
// another.
func forgetPresence(t *testing.T, orgID, userID string) {
	t.Helper()

	t.Cleanup(func() {
		opts, err := redis.ParseURL(envOr("TEAMUP_TEST_REDIS_URL", "redis://localhost:6379"))
		if err != nil {
			t.Fatalf("parse redis url: %v", err)
		}
		client := redis.NewClient(opts)
		defer client.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client.Del(ctx, "presence:sessions:"+userID, "presence:"+orgID+":"+userID)
	})
}

// The headline of issue #42: an event on one replica reached only the sockets
// on that replica, and the people on the other one saw nothing until they
// reloaded. No error, no log line, no metric.
func TestAnEventOnOneReplicaReachesSocketsOnTheOther(t *testing.T) {
	first, second := twoReplicas(t)

	token := first.login()
	userID, orgID := tokenSubject(t, token)
	forgetPresence(t, orgID, userID)

	_, watching := openSession(t, first, token)
	// Whatever this session was told about its own arrival.
	settle(watching, 2*time.Second)

	// A socket opening on the other replica is an event with no local cause on
	// this one: if it arrives here, it crossed.
	_, _ = openSession(t, second, token)

	frame := awaitFrame(t, watching, "presence.changed", 20*time.Second)

	var payload struct {
		UserID string `json:"userId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(frame.Payload, &payload); err != nil {
		t.Fatalf("decode presence.changed: %v", err)
	}
	if payload.UserID != userID {
		t.Errorf("presence arrived for the wrong person: got %q want %q", payload.UserID, userID)
	}
	if payload.Status != "online" {
		t.Errorf("presence status: got %q want online", payload.Status)
	}
}

// The other half of running two replicas: a socket closing on one of them must
// not publish somebody as offline while the other still has them. That was
// decided from one process's own map, so it was wrong exactly as often as
// somebody had two devices.
func TestASocketClosingOnOneReplicaDoesNotTakeThePersonOffline(t *testing.T) {
	first, second := twoReplicas(t)

	token := first.login()
	userID, orgID := tokenSubject(t, token)
	forgetPresence(t, orgID, userID)

	leaving, _ := openSession(t, first, token)
	staying, _ := openSession(t, second, token)

	if got := presenceStatus(t, orgID, userID); got != "online" {
		t.Fatalf("presence after connecting = %q, want online", got)
	}

	// One device goes: a tab closed, a laptop shut.
	_ = leaving.Close(websocket.StatusNormalClosure, "")

	// Long enough for the unregister to have happened and been wrong.
	time.Sleep(3 * time.Second)
	if got := presenceStatus(t, orgID, userID); got != "online" {
		t.Errorf("presence after one of two devices disconnected = %q, want online — "+
			"the replica that lost a socket published them as gone while the other still had them", got)
	}

	// And when the last one goes, they really have gone.
	_ = staying.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(20 * time.Second)
	for {
		if presenceStatus(t, orgID, userID) == "offline" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("their last socket closed and nobody published them as offline")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
