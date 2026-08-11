package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// serve runs the tracked handler on a throwaway request, as the router would.
func serve(t *testing.T, s *wsSessions, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/ws", nil)
	s.Track(handler)(c)
	return rec
}

// A session ends when the shutdown sequence says so, through the request
// context the handler is watching.
func TestWSSessionsCloseAllEndsLiveSessions(t *testing.T) {
	s := newWSSessions()

	const n = 3
	var wg sync.WaitGroup
	returned := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serve(t, s, func(c *gin.Context) {
				<-c.Request.Context().Done()
				returned <- struct{}{}
			})
		}()
	}

	waitForSessions(t, s, n)

	if closed := s.CloseAll(); closed != n {
		t.Errorf("CloseAll reported %d sessions, want %d", closed, n)
	}
	for i := 0; i < n; i++ {
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d session handlers returned", i, n)
		}
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Wait(ctx); err != nil {
		t.Errorf("Wait after CloseAll: %v", err)
	}
}

// Once the drain has started this instance is going away, so a client trying
// to open a socket is told to go elsewhere rather than being handed a session
// on a dying process.
func TestWSSessionsRefuseNewSessionsWhileDraining(t *testing.T) {
	s := newWSSessions()
	s.CloseAll()

	reached := false
	rec := serve(t, s, func(*gin.Context) { reached = true })

	if reached {
		t.Error("the websocket handler ran during the drain")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("a refusal during shutdown should tell the client when to come back")
	}
}

// Wait is what keeps the database and Redis open until the hub has finished
// unregistering. It must not return before the handlers do.
func TestWSSessionsWaitBlocksUntilHandlersReturn(t *testing.T) {
	s := newWSSessions()

	release := make(chan struct{})
	go serve(t, s, func(*gin.Context) { <-release })
	waitForSessions(t, s, 1)

	early, cancelEarly := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelEarly()
	if err := s.Wait(early); err == nil {
		t.Fatal("Wait returned while a session handler was still running")
	}

	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Wait(ctx); err != nil {
		t.Errorf("Wait after the handler returned: %v", err)
	}
}

// A handler that finishes on its own — the ordinary case, a client closing its
// tab — must leave nothing behind for the drain to wait on.
func TestWSSessionsForgetSessionsThatEndByThemselves(t *testing.T) {
	s := newWSSessions()
	serve(t, s, func(*gin.Context) {})

	if closed := s.CloseAll(); closed != 0 {
		t.Errorf("CloseAll found %d sessions after the handler returned, want 0", closed)
	}
}

func waitForSessions(t *testing.T, s *wsSessions, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := len(s.cancels)
		s.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("only saw fewer than %d tracked sessions", want)
}
