package observability

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Readiness answers "should this instance be sent traffic?".
//
// It is deliberately separate from liveness. Liveness asks whether the process
// is wedged, and a pod that answers no is restarted; readiness asks whether the
// dependencies a request needs are reachable, and a pod that answers no is
// simply taken out of the load balancer until they are. Wiring a dependency
// into liveness would restart pods over an outage they did not cause; leaving
// one out of readiness — which is what happened to object storage and the media
// server — routes traffic to an instance that answers 500.
//
// A readiness probe runs every couple of seconds on every replica, so results
// are cached for a short window and every dependency is probed under one
// deadline, concurrently. Without that, adding two dependencies would turn the
// probe itself into load on all four.
type Readiness struct {
	deps         []Dependency
	probeTimeout time.Duration
	cacheTTL     time.Duration

	// The lock is held across a refresh, so concurrent probes collapse onto one
	// set of checks rather than each starting their own.
	mu       sync.Mutex
	checked  time.Time
	response HealthResponse
	ready    bool
}

// Dependency is one thing readiness consults.
type Dependency struct {
	// Name appears in the response body, so an operator reading a 503 can see
	// which dependency is at fault without reaching for the logs.
	Name string
	// Check reports the dependency reachable by returning nil. It must respect
	// the context's deadline.
	Check func(ctx context.Context) error
}

const (
	defaultProbeTimeout = 2 * time.Second
	defaultCacheTTL     = 2 * time.Second
)

// NewReadiness builds a readiness probe over the given dependencies.
func NewReadiness(deps ...Dependency) *Readiness {
	return &Readiness{
		deps:         deps,
		probeTimeout: defaultProbeTimeout,
		cacheTTL:     defaultCacheTTL,
	}
}

// WithProbeTimeout bounds how long one round of checks may take.
func (r *Readiness) WithProbeTimeout(d time.Duration) *Readiness {
	if d > 0 {
		r.probeTimeout = d
	}
	return r
}

// WithCacheTTL sets how long a result is reused before the dependencies are
// consulted again. Zero disables caching, which is only sensible in tests.
func (r *Readiness) WithCacheTTL(d time.Duration) *Readiness {
	r.cacheTTL = d
	return r
}

// Handler is the gin handler for /readyz.
func (r *Readiness) Handler(c *gin.Context) {
	response, ready := r.Evaluate(c.Request.Context())
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, response)
}

// Evaluate returns the current readiness answer, refreshing it if the cached
// one has expired.
func (r *Readiness) Evaluate(ctx context.Context) (HealthResponse, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.checked.IsZero() && time.Since(r.checked) < r.cacheTTL {
		return r.response, r.ready
	}

	// The caller's context only bounds this probe from above; a client that
	// hangs up must not leave the next probe with a poisoned cache entry.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.probeTimeout)
	defer cancel()

	results := make([]CheckResult, len(r.deps))
	var wg sync.WaitGroup
	for i, dep := range r.deps {
		wg.Add(1)
		go func(i int, dep Dependency) {
			defer wg.Done()
			start := time.Now()
			err := dep.Check(probeCtx)
			results[i] = CheckResult{
				Status:  "pass",
				Latency: time.Since(start).Round(time.Millisecond).String(),
			}
			if err != nil {
				results[i].Status = "fail"
				results[i].Message = err.Error()
			}
		}(i, dep)
	}
	wg.Wait()

	checks := make(map[string]CheckResult, len(r.deps))
	ready := true
	for i, dep := range r.deps {
		checks[dep.Name] = results[i]
		if results[i].Status != "pass" {
			ready = false
		}
	}

	status := "ready"
	if !ready {
		status = "not ready"
	}

	r.response = HealthResponse{
		Status:    status,
		Checks:    checks,
		Timestamp: time.Now(),
		Version:   serviceVersion(),
	}
	r.ready = ready
	r.checked = time.Now()
	return r.response, r.ready
}

// PingCheck adapts anything with a context-taking Ping — the pgx pool, the
// files service — into a dependency.
func PingCheck(name string, pinger interface{ Ping(context.Context) error }) Dependency {
	return Dependency{Name: name, Check: pinger.Ping}
}

// HTTPCheck reports a dependency reachable when it answers an HTTP GET without
// a server error. It is how LiveKit is probed: the media server has no client
// library health call, but it answers its own HTTP root, and reaching that is
// exactly what the /livekit proxy needs to be able to do.
//
// The client keeps its connections alive between probes, so a probe every
// couple of seconds costs one request rather than one handshake.
func HTTPCheck(name, url string) Dependency {
	client := &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     90 * time.Second,
	}}
	return Dependency{
		Name: name,
		Check: func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("unreachable at %s: %w", url, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 500 {
				return fmt.Errorf("%s answered %s", url, resp.Status)
			}
			return nil
		},
	}
}

// ReadyHandler builds a readiness handler with the default cache and timeout.
func ReadyHandler(deps ...Dependency) gin.HandlerFunc {
	return NewReadiness(deps...).Handler
}
