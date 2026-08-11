package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func failing(name string, err error) Dependency {
	return Dependency{Name: name, Check: func(context.Context) error { return err }}
}

func passing(name string) Dependency {
	return Dependency{Name: name, Check: func(context.Context) error { return nil }}
}

// probe runs the handler and returns the status and decoded body.
func probe(t *testing.T, h gin.HandlerFunc) (int, HealthResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h(c)

	var body HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness body is not a health response: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, body
}

// An opaque 503 tells an operator that something is wrong and nothing about
// what. Readiness has to name the dependency and say what it saw.
func TestReadinessNamesTheDependencyThatFailed(t *testing.T) {
	r := NewReadiness(
		passing("database"),
		passing("redis"),
		failing("minio", errors.New("dial tcp 127.0.0.1:9099: connection refused")),
		passing("livekit"),
	)

	status, body := probe(t, r.Handler)

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if body.Status != "not ready" {
		t.Errorf("status field = %q, want %q", body.Status, "not ready")
	}
	minio, ok := body.Checks["minio"]
	if !ok {
		t.Fatalf("no minio entry in checks: %+v", body.Checks)
	}
	if minio.Status != "fail" {
		t.Errorf("minio check = %q, want fail", minio.Status)
	}
	if minio.Message == "" {
		t.Error("a failed check must carry the reason")
	}
	// The healthy dependencies are reported too, so the failing one stands out
	// rather than being the only thing anybody can see.
	for _, name := range []string{"database", "redis", "livekit"} {
		if body.Checks[name].Status != "pass" {
			t.Errorf("%s check = %q, want pass", name, body.Checks[name].Status)
		}
	}
}

func TestReadinessPassesWhenEveryDependencyPasses(t *testing.T) {
	r := NewReadiness(passing("database"), passing("redis"), passing("minio"), passing("livekit"))

	status, body := probe(t, r.Handler)

	if status != http.StatusOK {
		t.Errorf("status = %d, want %d", status, http.StatusOK)
	}
	if body.Status != "ready" {
		t.Errorf("status field = %q, want ready", body.Status)
	}
	if len(body.Checks) != 4 {
		t.Errorf("want all four dependencies reported, got %+v", body.Checks)
	}
}

// Kubernetes probes /readyz every couple of seconds on every replica. Opening
// a fresh conversation with four services each time is its own outage, so a
// result is reused for a short window.
func TestReadinessCachesWithinItsTTL(t *testing.T) {
	var calls atomic.Int64
	r := NewReadiness(Dependency{
		Name: "database",
		Check: func(context.Context) error {
			calls.Add(1)
			return nil
		},
	}).WithCacheTTL(time.Minute)

	for i := 0; i < 20; i++ {
		if status, _ := probe(t, r.Handler); status != http.StatusOK {
			t.Fatalf("probe %d: status = %d", i, status)
		}
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("dependency was checked %d times for 20 probes; want 1 within the cache TTL", got)
	}
}

// Cached, but not stale for ever: a dependency that comes back has to be
// noticed without a restart.
func TestReadinessRechecksAfterItsTTL(t *testing.T) {
	var healthy atomic.Bool
	r := NewReadiness(Dependency{
		Name: "minio",
		Check: func(context.Context) error {
			if healthy.Load() {
				return nil
			}
			return errors.New("connection refused")
		},
	}).WithCacheTTL(20 * time.Millisecond)

	if status, _ := probe(t, r.Handler); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 while the dependency is down", status)
	}

	healthy.Store(true)
	time.Sleep(40 * time.Millisecond)

	if status, _ := probe(t, r.Handler); status != http.StatusOK {
		t.Errorf("status = %d, want 200 once the dependency recovered", status)
	}
}

// A dependency that hangs must not hang the probe: an unanswered readiness
// check reads to the kubelet as a timeout, which is the same as unready but
// takes a probe period longer to say so.
func TestReadinessBoundsAHangingCheck(t *testing.T) {
	r := NewReadiness(Dependency{
		Name: "livekit",
		Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}).WithProbeTimeout(100 * time.Millisecond).WithCacheTTL(0)

	start := time.Now()
	status, body := probe(t, r.Handler)
	elapsed := time.Since(start)

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when a dependency never answers", status)
	}
	if body.Checks["livekit"].Status != "fail" {
		t.Errorf("livekit check = %+v, want fail", body.Checks["livekit"])
	}
	if elapsed > time.Second {
		t.Errorf("probe took %s; it must be bounded by the probe timeout", elapsed)
	}
}

// The checks share one deadline and run together. Four sequential 200ms checks
// would blow any sensible probe timeout.
func TestReadinessChecksDependenciesConcurrently(t *testing.T) {
	slow := func(name string) Dependency {
		return Dependency{Name: name, Check: func(ctx context.Context) error {
			select {
			case <-time.After(150 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
	}
	r := NewReadiness(slow("database"), slow("redis"), slow("minio"), slow("livekit")).
		WithProbeTimeout(2 * time.Second).WithCacheTTL(0)

	start := time.Now()
	status, _ := probe(t, r.Handler)
	elapsed := time.Since(start)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("four 150ms checks took %s; they are running one after another", elapsed)
	}
}

// A client that hangs up mid-probe must not leave its cancellation cached as
// the instance's readiness answer.
func TestReadinessIsNotPoisonedByAClientHangingUp(t *testing.T) {
	r := NewReadiness(passing("database")).WithCacheTTL(0)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx)
	r.Handler(c)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: a disconnected client is not an unhealthy dependency (%s)", rec.Code, rec.Body)
	}
}

// Readiness with nothing to check is ready. Belt and braces for the config
// where LiveKit is not set and its dependency is simply not registered.
func TestReadinessWithNoDependenciesIsReady(t *testing.T) {
	if status, body := probe(t, NewReadiness().Handler); status != http.StatusOK || body.Status != "ready" {
		t.Errorf("status = %d %q, want 200 ready", status, body.Status)
	}
}

// Liveness must never consult a dependency. A pod whose object storage is down
// is degraded, not wedged, and restarting it fixes nothing.
func TestLivenessDoesNotConsultDependencies(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/healthz", nil)

	start := time.Now()
	LiveHandler(c)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("liveness took %s; it is talking to something", elapsed)
	}
}

func TestHTTPCheckReportsAnUnreachableService(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer live.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := HTTPCheck("livekit", live.URL).Check(ctx); err != nil {
		t.Errorf("a service answering 200 is reachable, got %v", err)
	}
	if err := HTTPCheck("livekit", broken.URL).Check(ctx); err == nil {
		t.Error("a service answering 500 must not count as reachable")
	}
	if err := HTTPCheck("livekit", deadURL).Check(ctx); err == nil {
		t.Error("a service that is not listening must not count as reachable")
	}
}
