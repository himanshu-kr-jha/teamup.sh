package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"nhooyr.io/websocket"
)

// SIGTERM has to drain, not abandon. The server used to hand srv.Shutdown to a
// goroutine nobody joined: ListenAndServe returned the instant shutdown began,
// runServer fell through, the deferred pool closes ran, and the process was
// gone about a tenth of a second later — with requests still in flight and
// websocket sessions still open.
//
// This starts the real binary, puts a request in flight, opens websocket
// sessions, and measures what SIGTERM actually does to them.
func TestSIGTERMDrainsInFlightRequestsAndClosesSessions(t *testing.T) {
	const (
		bodyDuration = 2 * time.Second
		grace        = 15 * time.Second
	)

	srv := startServer(t, serverOptions{
		autoSetup: true,
		waitReady: true,
		env: map[string]string{
			"TEAMUP_SHUTDOWN_GRACE": grace.String(),
		},
	})

	token := srv.login()
	userID, orgID := tokenSubject(t, token)

	// Three sessions, as in the reproduction on the ticket.
	sessions := openSessions(t, srv, token, 3)

	// The hub records the user online on register. Whether it gets to record
	// them offline again is the whole question.
	if got := presenceStatus(t, orgID, userID); got != "online" {
		t.Fatalf("presence after connecting = %q, want online", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inFlight := srv.slowBodyRequest(ctx, bodyDuration)

	// Let the request get past the handler's entry and block on its body.
	time.Sleep(400 * time.Millisecond)

	signalledAt, exitAfter, exited := srv.terminate()
	if !exited {
		t.Fatalf("server never exited after SIGTERM\n%s", srv.logs.String())
	}
	t.Logf("process exited %s after SIGTERM", exitAfter.Round(time.Millisecond))

	var got slowResult
	select {
	case got = <-inFlight:
	case <-time.After(30 * time.Second):
		t.Fatal("the in-flight request never resolved")
	}
	t.Logf("in-flight request: status=%d err=%v after=%s", got.status, got.err, got.took.Round(time.Millisecond))

	// 1. The request that was already being served must be answered.
	if got.err != nil {
		t.Errorf("in-flight request was severed by shutdown: %v (server log:\n%s)", got.err, srv.logs.String())
	} else if got.status != http.StatusUnauthorized {
		// Deliberately bad credentials: the point is a complete response, and
		// 401 is the complete response for them.
		t.Errorf("in-flight request returned %d, want %d — body: %s", got.status, http.StatusUnauthorized, got.body)
	}

	// 2. The process must outlive the drain rather than racing it. The body
	//    still had ~1.6s to dribble in when SIGTERM arrived.
	const minimumDrain = 1200 * time.Millisecond
	if exitAfter < minimumDrain {
		t.Errorf("process exited %s after SIGTERM, before the in-flight request could finish; want at least %s",
			exitAfter.Round(time.Millisecond), minimumDrain)
	}

	// 3. And it must still be bounded by the grace period.
	if limit := grace + 10*time.Second; exitAfter > limit {
		t.Errorf("process took %s to exit, past the %s grace period", exitAfter.Round(time.Millisecond), grace)
	}

	// 4. Websocket sessions must be closed by the shutdown sequence, while the
	//    server is still up and can unregister them — not by the process
	//    disappearing out from under them.
	for _, s := range sessions {
		closedAt, err := s.closure(10 * time.Second)
		if err != nil {
			t.Errorf("session %d never observed a close: %v", s.index, err)
			continue
		}
		afterSignal := closedAt.Sub(signalledAt)
		beforeExit := exitAfter - afterSignal
		t.Logf("session %d closed %s after SIGTERM (%s before the process exited): %v",
			s.index, afterSignal.Round(time.Millisecond), beforeExit.Round(time.Millisecond), s.readErr)
		if beforeExit < 500*time.Millisecond {
			t.Errorf("session %d was dropped by the process exiting (closed %s after SIGTERM, process exited at %s), "+
				"not closed by the shutdown sequence",
				s.index, afterSignal.Round(time.Millisecond), exitAfter.Round(time.Millisecond))
		}
	}

	// 5. And closing them early is what makes the hub's unregister possible:
	//    it writes presence, so it needs Redis still open. A process that just
	//    disappears leaves everybody who was connected to it recorded online.
	if got := presenceStatus(t, orgID, userID); got != "offline" {
		t.Errorf("presence after shutdown = %q, want offline — the sessions were never unregistered", got)
	}
}

// tokenSubject reads the user and org out of an access token. The signature is
// the server's business; the test only needs the claims to find its own
// presence key.
func tokenSubject(t *testing.T, token string) (userID, orgID string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("access token is not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode token claims: %v", err)
	}
	var claims struct {
		UserID string `json:"user_id"`
		OrgID  string `json:"org_id"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("parse token claims: %v", err)
	}
	if claims.UserID == "" || claims.OrgID == "" {
		t.Fatalf("token carries no user or org: %s", raw)
	}
	return claims.UserID, claims.OrgID
}

// presenceStatus reads the status the hub recorded for a user, from the same
// Redis the server writes to.
func presenceStatus(t *testing.T, orgID, userID string) string {
	t.Helper()
	opts, err := redis.ParseURL(envOr("TEAMUP_TEST_REDIS_URL", "redis://localhost:6379"))
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := client.HGet(ctx, "presence:"+orgID+":"+userID, "status").Result()
	if errors.Is(err, redis.Nil) {
		return ""
	}
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	return status
}

type wsSession struct {
	index    int
	conn     *websocket.Conn
	done     chan struct{}
	once     sync.Once
	closedAt time.Time
	readErr  error
}

// closure waits for the session's read to fail and reports when it did.
func (s *wsSession) closure(timeout time.Duration) (time.Time, error) {
	select {
	case <-s.done:
		return s.closedAt, nil
	case <-time.After(timeout):
		return time.Time{}, context.DeadlineExceeded
	}
}

func openSessions(t *testing.T, srv *testServer, token string, n int) []*wsSession {
	t.Helper()
	sessions := make([]*wsSession, 0, n)
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		conn, _, err := websocket.Dial(ctx, srv.baseURL+"/ws", &websocket.DialOptions{
			HTTPHeader: http.Header{"Cookie": []string{"at=" + token}},
		})
		cancel()
		if err != nil {
			t.Fatalf("open websocket %d: %v\n%s", i, err, srv.logs.String())
		}
		s := &wsSession{index: i, conn: conn, done: make(chan struct{})}
		sessions = append(sessions, s)
		t.Cleanup(func() { _ = conn.CloseNow() })

		go func(s *wsSession) {
			for {
				// Long-lived read: the only thing that ends it is the server
				// closing the session.
				if _, _, err := s.conn.Read(context.Background()); err != nil {
					s.once.Do(func() {
						s.closedAt = time.Now()
						s.readErr = err
						close(s.done)
					})
					return
				}
			}
		}(s)
	}
	return sessions
}
