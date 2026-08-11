package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// wsSessions tracks live WebSocket requests so that shutting down can end them.
//
// A WebSocket is a hijacked connection. net/http stops tracking a connection
// the moment a handler hijacks it, which means http.Server.Shutdown neither
// waits for the session nor closes it — the socket simply dies with the
// process, and every client of the departing instance reconnects at once on an
// abnormal-closure path.
//
// Ending the session through its request context, rather than by exiting, is
// what lets the hub run its unregister: presence flips to offline and the
// user's colleagues are told, all while the database and Redis are still open.
type wsSessions struct {
	mu       sync.Mutex
	draining bool
	next     uint64
	cancels  map[uint64]context.CancelFunc
	live     sync.WaitGroup
}

func newWSSessions() *wsSessions {
	return &wsSessions{cancels: make(map[uint64]context.CancelFunc)}
}

// Track wraps the WebSocket route so each session can be ended on shutdown.
func (s *wsSessions) Track(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()

		id, ok := s.add(cancel)
		if !ok {
			// The drain has begun; this instance is going away. Answering
			// rather than accepting means the client retries somewhere else
			// instead of opening a session onto a dying process.
			c.Header("Retry-After", "5")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server is shutting down"})
			return
		}
		defer s.remove(id)

		c.Request = c.Request.WithContext(ctx)
		next(c)
	}
}

func (s *wsSessions) add(cancel context.CancelFunc) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return 0, false
	}
	s.next++
	id := s.next
	s.cancels[id] = cancel
	// Registered under the same lock that CloseAll takes, so a session can
	// never join the WaitGroup after the drain has started counting.
	s.live.Add(1)
	return id, true
}

func (s *wsSessions) remove(id uint64) {
	s.mu.Lock()
	_, tracked := s.cancels[id]
	delete(s.cancels, id)
	s.mu.Unlock()
	if tracked {
		s.live.Done()
	}
}

// CloseAll stops accepting sessions and ends the ones that are open. It
// returns how many it ended.
func (s *wsSessions) CloseAll() int {
	s.mu.Lock()
	s.draining = true
	cancels := make([]context.CancelFunc, 0, len(s.cancels))
	for _, cancel := range s.cancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

// Wait blocks until every session handler has returned, or the context expires.
func (s *wsSessions) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.live.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
