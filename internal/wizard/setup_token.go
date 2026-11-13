package wizard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Setup creates the organization and its owner, and before it is finished
// nobody is signed in to stop anybody else doing it. On a fresh server the
// first stranger to open /wizard would own it. So setup asks for a code only
// the person running the server can see: the server prints it to its own log
// at start, and every step must carry it.

// SetupTokenHeader carries the code on every setup step.
const SetupTokenHeader = "X-Setup-Token"

const setupTokenKey = "wizard:setup-token"

// setupTokens keeps the one code, in Redis so every server agrees on it, or
// in this process when there is no Redis.
type setupTokens struct {
	redis *redis.Client
	mu    sync.Mutex
	local string
}

// SetupToken is the code setup needs, made once and kept until setup is done.
func (s *Service) SetupToken(ctx context.Context) (string, error) {
	t := &s.tokens
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	fresh := base64.RawURLEncoding.EncodeToString(b)
	if t.redis != nil {
		// Whichever server starts first makes it; the rest read that one.
		if err := t.redis.SetNX(ctx, setupTokenKey, fresh, 0).Err(); err != nil {
			return "", err
		}
		return t.redis.Get(ctx, setupTokenKey).Result()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.local == "" {
		t.local = fresh
	}
	return t.local, nil
}

// forgetSetupToken drops the code once setup is done; it is no use after.
func (s *Service) forgetSetupToken(ctx context.Context) {
	if s.tokens.redis != nil {
		_ = s.tokens.redis.Del(ctx, setupTokenKey).Err()
		return
	}
	s.tokens.mu.Lock()
	s.tokens.local = ""
	s.tokens.mu.Unlock()
}

// requireSetupToken refuses a setup step without the code.
func requireSetupToken(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		want, err := service.SetupToken(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "could not check the setup code"})
			return
		}
		got := c.GetHeader(SetupTokenHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":      "enter the setup code the server printed in its log",
				"setupToken": true,
			})
			return
		}
		c.Next()
	}
}
