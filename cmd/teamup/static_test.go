package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestStaticServesEveryFolderAndRootFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	site := fstest.MapFS{
		"index.html":              {Data: []byte("<html>app</html>")},
		"sw.js":                   {Data: []byte("worker")},
		"assets/index-abc.js":     {Data: []byte("bundle")},
		"sounds/ringtone.mp3":     {Data: []byte("ring")},
		"backgrounds/night.jpg":   {Data: []byte("night")},
		"mediapipe/selfie.tflite": {Data: []byte("model")},
		"mediapipe/tasks-vision-1.2/wasm/vision.wasm": {Data: []byte("\x00asm")},
	}
	router := gin.New()
	registerStatic(router, site)

	cases := []struct {
		path, body, cache string
	}{
		{"/sw.js", "worker", ""},
		{"/assets/index-abc.js", "bundle", "public, max-age=31536000, immutable"},
		{"/sounds/ringtone.mp3", "ring", "public, max-age=86400"},
		{"/backgrounds/night.jpg", "night", "public, max-age=86400"},
		{"/mediapipe/selfie.tflite", "model", "public, max-age=86400"},
		{"/mediapipe/tasks-vision-1.2/wasm/vision.wasm", "\x00asm", "public, max-age=31536000, immutable"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != c.body {
			t.Errorf("%s: got %d %q, want 200 %q", c.path, rec.Code, rec.Body.String(), c.body)
		}
		if got := rec.Header().Get("Cache-Control"); got != c.cache {
			t.Errorf("%s: Cache-Control %q, want %q", c.path, got, c.cache)
		}
	}
	// The runtime is served as WebAssembly, which browsers insist on to compile it as it streams.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mediapipe/tasks-vision-1.2/wasm/vision.wasm", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/wasm" {
		t.Errorf("wasm Content-Type %q", ct)
	}
	// A file that is not there is a 404, not the app.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sounds/missing.mp3", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing file: got %d", rec.Code)
	}
}
