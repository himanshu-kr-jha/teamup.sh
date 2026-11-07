package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeReadyFollowsTheStatusCode(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))

	if err := probeReady(srv.URL); err != nil {
		t.Fatalf("a 200 is ready, got %v", err)
	}

	// The readiness handler answers 503 while a dependency is down.
	status = http.StatusServiceUnavailable
	if err := probeReady(srv.URL); err == nil {
		t.Fatal("a 503 must not count as ready")
	}

	srv.Close()
	if err := probeReady(srv.URL); err == nil {
		t.Fatal("a server that is not listening must not count as ready")
	}
}
