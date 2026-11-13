package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("TEAMUP_TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("redis url: %v", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("no test Redis at %s: %v", url, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// both runs a test against Redis and against the in-process fallback, with a
// clock the test moves.
func both(t *testing.T, fn func(t *testing.T, l *Limiter, clock *time.Time)) {
	for name, client := range map[string]func(*testing.T) *redis.Client{
		"redis":   testRedis,
		"process": func(*testing.T) *redis.Client { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			l := New(client(t), zerolog.Nop())
			// The start of a window, so the previous one weighs nothing.
			clock := time.Now().Truncate(time.Hour).Add(time.Hour)
			l.now = func() time.Time { return clock }
			fn(t, l, &clock)
		})
	}
}

func TestALimitLetsThatManyThroughAndNoMore(t *testing.T) {
	both(t, func(t *testing.T, l *Limiter, _ *time.Time) {
		name, who := "t-"+uuid.NewString(), "someone"
		limit := Limit{N: 5, Per: time.Minute}
		for i := range 5 {
			if ok, _ := l.Allow(context.Background(), name, who, limit); !ok {
				t.Fatalf("request %d of 5 was refused", i+1)
			}
		}
		ok, wait := l.Allow(context.Background(), name, who, limit)
		if ok {
			t.Fatal("the sixth went through")
		}
		if wait <= 0 || wait > time.Minute {
			t.Fatalf("wait %v, want within the minute", wait)
		}
		if ok, _ := l.Allow(context.Background(), name, "somebody else", limit); !ok {
			t.Fatal("somebody else was refused for the first one's count")
		}
	})
}

func TestTheWindowSlidesRatherThanResettingAtItsEdge(t *testing.T) {
	both(t, func(t *testing.T, l *Limiter, clock *time.Time) {
		name, who := "t-"+uuid.NewString(), "someone"
		limit := Limit{N: 10, Per: time.Minute}
		*clock = clock.Add(50 * time.Second)
		for range 10 {
			l.Allow(context.Background(), name, who, limit)
		}
		// Just past the edge a fixed window would allow ten more at once.
		*clock = clock.Add(15 * time.Second)
		let := 0
		for range 10 {
			if ok, _ := l.Allow(context.Background(), name, who, limit); ok {
				let++
			}
		}
		if let > 2 {
			t.Fatalf("%d of 10 went through just after the edge, want at most 2", let)
		}
		// A whole window later it is fresh.
		*clock = clock.Add(2 * time.Minute)
		if ok, _ := l.Allow(context.Background(), name, who, limit); !ok {
			t.Fatal("refused a whole window later")
		}
	})
}

func TestTheMiddlewareRefusesWith429AndSaysWhenToComeBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	both(t, func(t *testing.T, l *Limiter, _ *time.Time) {
		name := "t-" + uuid.NewString()
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_id", c.GetHeader("X-User")); c.Next() })
		r.Use(l.Handler(Rule{Name: name, Method: http.MethodPost, Route: "/send/:id", Limit: Limit{N: 2, Per: time.Minute}, Who: ByUser}))
		r.POST("/send/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		r.GET("/read", func(c *gin.Context) { c.Status(http.StatusNoContent) })

		do := func(method, path, user string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("X-User", user)
			r.ServeHTTP(w, req)
			return w
		}
		// The route pattern is what counts, not the id in it.
		do("POST", "/send/a", "ann")
		do("POST", "/send/b", "ann")
		w := do("POST", "/send/c", "ann")
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("third send: %d, want 429", w.Code)
		}
		if w.Header().Get("Retry-After") == "" {
			t.Fatal("no Retry-After")
		}
		if got := do("POST", "/send/a", "bob").Code; got != http.StatusNoContent {
			t.Fatalf("bob was limited by ann's count: %d", got)
		}
		if got := do("GET", "/read", "ann").Code; got != http.StatusNoContent {
			t.Fatalf("a route with no rule was limited: %d", got)
		}
	})
}

func TestRedisBeingDownLetsRequestsThrough(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	defer client.Close()
	l := New(client, zerolog.Nop())
	for range 3 {
		if ok, _ := l.Allow(context.Background(), "t", "x", Limit{N: 1, Per: time.Minute}); !ok {
			t.Fatal("refused because the count could not be read")
		}
	}
}
