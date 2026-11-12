package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	// loginFailuresAllowed is how many wrong passwords an account takes in
	// loginFailureWindow before signing in to it is refused for the rest of it.
	loginFailuresAllowed = 10
	loginFailureWindow   = 15 * time.Minute
)

// dummyHash is compared against when there is no such account, so an unknown
// address takes as long to refuse as a wrong password does and the time taken
// does not say who has an account.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("no such account, only here to spend the same time"), bcrypt.DefaultCost)

// spendCompareTime takes the time a password check takes, and checks nothing.
func spendCompareTime(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

// accountKey names an account in the counter without keeping its address.
func accountKey(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "login-failures:" + hex.EncodeToString(sum[:16])
}

// failures counts wrong passwords per account, in Redis so every server sees
// the same count, or in this process when there is no Redis. The limit nginx
// puts on each address does not stop guesses spread over many addresses at
// one account; this does.
type failures struct {
	mu    sync.Mutex
	local map[string][]time.Time
}

func (s *Service) tooManyFailures(ctx context.Context, email string) bool {
	key := accountKey(email)
	if s.redis != nil {
		n, err := s.redis.Get(ctx, key).Int()
		return err == nil && n >= loginFailuresAllowed
	}
	s.fails.mu.Lock()
	defer s.fails.mu.Unlock()
	return len(s.fails.recent(key)) >= loginFailuresAllowed
}

func (s *Service) recordFailure(ctx context.Context, email string) {
	key := accountKey(email)
	if s.redis != nil {
		pipe := s.redis.TxPipeline()
		pipe.Incr(ctx, key)
		pipe.ExpireNX(ctx, key, loginFailureWindow)
		_, _ = pipe.Exec(ctx)
		return
	}
	s.fails.mu.Lock()
	defer s.fails.mu.Unlock()
	if s.fails.local == nil {
		s.fails.local = map[string][]time.Time{}
	}
	s.fails.local[key] = append(s.fails.recent(key), time.Now())
}

func (s *Service) clearFailures(ctx context.Context, email string) {
	key := accountKey(email)
	if s.redis != nil {
		_ = s.redis.Del(ctx, key).Err()
		return
	}
	s.fails.mu.Lock()
	defer s.fails.mu.Unlock()
	delete(s.fails.local, key)
}

// recent is the failures still inside the window; the caller holds mu.
func (f *failures) recent(key string) []time.Time {
	var keep []time.Time
	for _, at := range f.local[key] {
		if time.Since(at) < loginFailureWindow {
			keep = append(keep, at)
		}
	}
	return keep
}

// OnSessionsEnded adds something to run when every session somebody has has
// been ended, as a password change ends them: closing their live connections.
func (s *Service) OnSessionsEnded(fn func(userID string)) {
	s.sessionsEndedHooks = append(s.sessionsEndedHooks, fn)
}

func (s *Service) sessionsEnded(userID string) {
	for _, fn := range s.sessionsEndedHooks {
		fn(userID)
	}
}
