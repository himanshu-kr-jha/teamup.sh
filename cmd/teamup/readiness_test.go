package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// /readyz used to consult Postgres and Redis and nothing else, so an instance
// that could not reach object storage or the media server still reported itself
// ready — and Kubernetes routed traffic to it, where uploads and call creation
// answered 500. Readiness has to cover every dependency a request needs.
//
// Liveness is the opposite: /healthz must keep answering 200 through all of
// this, or the kubelet restarts a pod whose only problem is a sick neighbour.

func TestReadyzFailsWhenMinIOIsUnreachable(t *testing.T) {
	dead := "127.0.0.1:" + freePort(t)
	srv := startServer(t, serverOptions{
		env: map[string]string{"TEAMUP_MINIO_ENDPOINT": dead},
	})

	if status, body := srv.get("/healthz"); status != http.StatusOK {
		t.Errorf("liveness must not fail because object storage is down: /healthz = %d %s", status, body)
	}

	status, body := srv.get("/readyz")
	t.Logf("/readyz with MinIO at %s: %d %s", dead, status, body)
	if status != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d, want %d with object storage unreachable — body: %s",
			status, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "minio") {
		t.Errorf("/readyz body must name the failing dependency, got: %s", body)
	}
}

func TestReadyzFailsWhenLiveKitIsUnreachable(t *testing.T) {
	dead := "ws://127.0.0.1:" + freePort(t)
	srv := startServer(t, serverOptions{
		env: map[string]string{"TEAMUP_LIVEKIT_URL": dead},
	})

	if status, body := srv.get("/healthz"); status != http.StatusOK {
		t.Errorf("liveness must not fail because the media server is down: /healthz = %d %s", status, body)
	}

	status, body := srv.get("/readyz")
	t.Logf("/readyz with LiveKit at %s: %d %s", dead, status, body)
	if status != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d, want %d with LiveKit unreachable — body: %s",
			status, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "livekit") {
		t.Errorf("/readyz body must name the failing dependency, got: %s", body)
	}
}

// With all four dependencies up, readiness passes and says so per dependency,
// and repeated probes are served from the cache rather than reconnecting to
// four services every time.
func TestReadyzPassesWithEveryDependencyUpAndIsCheapToPoll(t *testing.T) {
	srv := startServer(t, serverOptions{waitReady: true})

	status, body := srv.get("/readyz")
	t.Logf("/readyz healthy: %d %s", status, body)
	if status != http.StatusOK {
		t.Fatalf("/readyz = %d with every dependency live — body: %s\n%s", status, body, srv.logs.String())
	}
	for _, dep := range []string{"database", "redis", "minio", "livekit"} {
		if !strings.Contains(body, dep) {
			t.Errorf("/readyz body should report %q, got: %s", dep, body)
		}
	}

	// A probe every second or two per replica must not turn into a load test
	// against Postgres, Redis, MinIO and LiveKit.
	const probes = 30
	start := time.Now()
	for i := 0; i < probes; i++ {
		if status, body := srv.get("/readyz"); status != http.StatusOK {
			t.Fatalf("probe %d: /readyz = %d %s", i, status, body)
		}
	}
	perProbe := time.Since(start) / probes
	t.Logf("%d readiness probes averaged %s each", probes, perProbe.Round(time.Microsecond))
	if perProbe > 10*time.Millisecond {
		t.Errorf("readiness averaged %s per probe; it is re-dialling its dependencies on every request", perProbe)
	}
}
