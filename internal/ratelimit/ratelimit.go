// Package ratelimit caps how often one person, or one address, may do a thing.
//
// Counts live in Redis so every server sees the same ones, or in this process
// when there is no Redis. Each limit is a sliding window: the count of the
// current window plus the part of the previous one still inside it, which
// stops the double burst a plain fixed window allows at its edge.
//
// A limit that cannot be checked, because Redis is down, lets the request
// through and says so in the log: being unable to count is no reason to stop
// everybody working.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Limit is at most N in every Per.
type Limit struct {
	N   int
	Per time.Duration
}

// Limiter counts.
type Limiter struct {
	redis  *redis.Client
	logger zerolog.Logger
	now    func() time.Time

	mu    sync.Mutex
	local map[string]*count
	swept time.Time
}

// count is one window's count kept in this process, and when it stops mattering.
type count struct {
	n       int
	expires time.Time
}

// New counts in client, or in this process when client is nil.
func New(client *redis.Client, logger zerolog.Logger) *Limiter {
	return &Limiter{redis: client, logger: logger, now: time.Now, local: map[string]*count{}}
}

// Allow counts one more of name for who, and says whether it is within limit,
// and if not, how long until it would be.
func (l *Limiter) Allow(ctx context.Context, name, who string, limit Limit) (bool, time.Duration) {
	now := l.now()
	window := limit.Per
	start := now.Truncate(window)
	cur := key(name, who, start)
	prev := key(name, who, start.Add(-window))

	var current, previous int
	if l.redis != nil {
		pipe := l.redis.TxPipeline()
		incr := pipe.Incr(ctx, cur)
		pipe.ExpireNX(ctx, cur, 2*window)
		before := pipe.Get(ctx, prev)
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			l.logger.Warn().Err(err).Str("limit", name).Msg("could not count against a rate limit; letting it through")
			return true, 0
		}
		current = int(incr.Val())
		previous, _ = before.Int()
	} else {
		l.mu.Lock()
		l.sweep(now)
		c := l.local[cur]
		if c == nil {
			c = &count{expires: start.Add(2 * window)}
			l.local[cur] = c
		}
		c.n++
		current = c.n
		if p := l.local[prev]; p != nil {
			previous = p.n
		}
		l.mu.Unlock()
	}

	// How much of the previous window still lies inside the sliding one.
	weight := 1 - float64(now.Sub(start))/float64(window)
	if float64(current)+math.Floor(float64(previous)*weight) <= float64(limit.N) {
		return true, 0
	}
	return false, start.Add(window).Sub(now)
}

// sweep forgets local counts too old to matter, once a minute; the caller holds mu.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.swept) < time.Minute {
		return
	}
	l.swept = now
	for k, c := range l.local {
		if now.After(c.expires) {
			delete(l.local, k)
		}
	}
}

func key(name, who string, start time.Time) string {
	return "rl:" + name + ":" + who + ":" + strconv.FormatInt(start.Unix(), 10)
}

// Who a limit counts against.
type Who func(c *gin.Context) string

// ByUser counts per signed-in person, or per address for anybody else.
func ByUser(c *gin.Context) string {
	if id := c.GetString("user_id"); id != "" {
		return "u:" + id
	}
	return ByAddress(c)
}

// ByAddress counts per client address, kept as a hash so the keys in Redis
// say nothing about who visited.
func ByAddress(c *gin.Context) string {
	sum := sha256.Sum256([]byte(c.ClientIP()))
	return "a:" + hex.EncodeToString(sum[:12])
}

// Rule is one limit on one route, or on every route when Route is empty.
type Rule struct {
	// Name keys the count; two rules with one name share it.
	Name string
	// Method and Route are the route as registered, such as POST and
	// /api/channels/:id/messages. Route "" applies to everything this
	// middleware sees.
	Method, Route string
	// Prefix narrows a rule with no Route to paths starting with it.
	Prefix string
	Limit  Limit
	Who    Who
}

// Handler applies every rule that matches the request, in order, and refuses
// with 429 and Retry-After at the first one used up.
func (l *Limiter) Handler(rules ...Rule) gin.HandlerFunc {
	byRoute := map[string][]Rule{}
	var always []Rule
	for _, r := range rules {
		if r.Route == "" {
			always = append(always, r)
			continue
		}
		byRoute[r.Method+" "+r.Route] = append(byRoute[r.Method+" "+r.Route], r)
	}
	return func(c *gin.Context) {
		matched := byRoute[c.Request.Method+" "+c.FullPath()]
		for _, set := range [][]Rule{always, matched} {
			for _, r := range set {
				if r.Prefix != "" && !strings.HasPrefix(c.Request.URL.Path, r.Prefix) {
					continue
				}
				ok, wait := l.Allow(c.Request.Context(), r.Name, r.Who(c), r.Limit)
				if ok {
					continue
				}
				secs := int(math.Ceil(wait.Seconds()))
				if secs < 1 {
					secs = 1
				}
				l.logger.Warn().Str("limit", r.Name).Str("route", c.FullPath()).Str("who", r.Who(c)).Msg("rate limited")
				c.Header("Retry-After", strconv.Itoa(secs))
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": fmt.Sprintf("that is too many at once; try again in %s", humanWait(secs)),
				})
				return
			}
		}
		c.Next()
	}
}

// Routes is every route a rule names, to check them against what is registered.
func Routes(rules []Rule) []string {
	var out []string
	for _, r := range rules {
		if r.Route != "" {
			out = append(out, r.Method+" "+r.Route)
		}
	}
	return out
}

func humanWait(secs int) string {
	switch {
	case secs < 60:
		if secs == 1 {
			return "a second"
		}
		return strconv.Itoa(secs) + " seconds"
	case secs < 3600:
		m := (secs + 59) / 60
		if m == 1 {
			return "a minute"
		}
		return strconv.Itoa(m) + " minutes"
	default:
		h := (secs + 3599) / 3600
		if h == 1 {
			return "an hour"
		}
		return strconv.Itoa(h) + " hours"
	}
}
