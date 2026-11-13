package websocket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"

	"github.com/teamup/teamup/pkg/protocol"
)

// A script on a stolen session could write frames as fast as the network
// allows, and each one makes the server check permissions and fan out to a
// whole channel. One that goes on past its limit is closed.
func TestASocketSendingFlatOutIsClosed(t *testing.T) {
	pool := newSockets(t)
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	conn, client := pool.open(t, hub, "conn-1", "user-1", "org-1")
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Read in the background so the close frame is seen.
	closed := make(chan error, 1)
	go func() {
		for {
			if _, _, err := client.Read(ctx); err != nil {
				closed <- err
				return
			}
		}
	}()
	for range 1000 {
		if err := client.Write(ctx, websocket.MessageText, []byte(`{"type":"conn.pong","payload":{}}`)); err != nil {
			break
		}
	}
	select {
	case err := <-closed:
		if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation {
			t.Fatalf("closed with %v (%v), want policy violation", status, err)
		}
	case <-ctx.Done():
		t.Fatal("a socket sending 1000 frames at once was never closed")
	}
}

func TestAPersonsPaceIsWellInsideTheFrameLimit(t *testing.T) {
	var b frameBucket
	at := time.Now()
	// Ten frames a second for a minute: typing, reading, sending at once.
	for i := range 600 {
		if !b.take(at) {
			t.Fatalf("frame %d at ten a second was refused", i)
		}
		at = at.Add(100 * time.Millisecond)
	}
	// And a burst on reconnect, resubscribing to everything.
	for i := range frameBurst {
		if !b.take(at) {
			t.Fatalf("frame %d of a reconnect burst was refused", i)
		}
	}
}

func TestStartedTypingGoesOutAtMostEveryFewSeconds(t *testing.T) {
	c := &Connection{}
	typing := func(channel string, on bool) *protocol.Message {
		state := "false"
		if on {
			state = "true"
		}
		return &protocol.Message{Type: protocol.TypeChatTyping, Payload: []byte(`{"channelId":"` + channel + `","isTyping":` + state + `}`)}
	}
	now := time.Now()
	if c.typingTooSoon(typing("a", true), now) {
		t.Fatal("the first was held back")
	}
	if !c.typingTooSoon(typing("a", true), now.Add(time.Second)) {
		t.Fatal("a second one a second later went out")
	}
	if c.typingTooSoon(typing("b", true), now.Add(time.Second)) {
		t.Fatal("another conversation was held back by the first")
	}
	if c.typingTooSoon(typing("a", false), now.Add(time.Second)) {
		t.Fatal("stopped typing was held back, leaving them shown typing")
	}
	if c.typingTooSoon(typing("a", true), now.Add(typingEvery+time.Millisecond)) {
		t.Fatal("held back after the interval")
	}
	// Stopped, then started again at once (the box cleared and typed into):
	// the app says "started" only once, so this one must go out.
	later := now.Add(typingEvery + 2*time.Millisecond)
	c.typingTooSoon(typing("a", false), later)
	if c.typingTooSoon(typing("a", true), later.Add(100*time.Millisecond)) {
		t.Fatal("a fresh start after a stop was held back, so nobody sees them typing")
	}
}

type stubAuth struct{}

func (stubAuth) ValidateAccessToken(_ context.Context, token string) (*Claims, error) {
	if token != "good" {
		return nil, errors.New("bad token")
	}
	return &Claims{UserID: "user-1", OrgID: "org-1", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestOnePersonCannotHoldOpenUnlimitedSockets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	for i := range maxSocketsPerUser {
		hub.Register(testConnection(hub, "c"+string(rune('a'+i)), "user-1", "org-1"))
	}
	r := gin.New()
	r.GET("/ws", WSHandler(hub, stubAuth{}, nil))
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ws", nil)
	req.Header.Set("Authorization", "Bearer good")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("socket %d: %d, want 429", maxSocketsPerUser+1, res.StatusCode)
	}
}

func TestATokenInTheAddressIsNotAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	r := gin.New()
	r.GET("/ws", WSHandler(hub, stubAuth{}, nil))
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws?token=good", nil)
	if err == nil {
		t.Fatal("a socket opened with the token in its address")
	}
	if res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %v, want 401", res)
	}
}
