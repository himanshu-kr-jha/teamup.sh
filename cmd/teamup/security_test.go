package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestThePolicyLetsOnlyThePagesOwnInlineScriptRun(t *testing.T) {
	page := []byte("<head><script>var a = 1;</script></head><body><script type=\"module\" src=\"/x.js\"></script></body>")
	csp := contentSecurityPolicy(page, "localhost:9000", false, "http://localhost:7880", "")
	// sha256 of "var a = 1;"
	if !strings.Contains(csp, "'sha256-") {
		t.Fatalf("no hash for the inline script: %s", csp)
	}
	if strings.Contains(csp, "'unsafe-inline' 'self' 'wasm") || strings.Contains(strings.Split(strings.Split(csp, "script-src ")[1], ";")[0], "unsafe-inline") {
		t.Errorf("inline script allowed wholesale: %s", csp)
	}
	for _, want := range []string{"object-src 'none'", "frame-ancestors 'self'", "base-uri 'self'", "http://localhost:9000", "ws://localhost:7880", "http://localhost:7880", "form-action 'self' https://github.com"} {
		if !strings.Contains(csp, want) {
			t.Errorf("missing %q in %s", want, csp)
		}
	}
}

func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders("default-src 'self'"))
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Permissions-Policy", "Cross-Origin-Opener-Policy"} {
		if w.Header().Get(h) == "" {
			t.Errorf("no %s", h)
		}
	}
}

func TestAnAPIRequestCannotSendMegabytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(limitBodies())
	read := func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	}
	r.POST("/api/messages", read)
	r.POST("/api/integrations/github/hook/:key", read)
	big := strings.Repeat("x", maxBody+1)
	for path, want := range map[string]int{
		"/api/messages":                     http.StatusRequestEntityTooLarge,
		"/api/integrations/github/hook/abc": http.StatusOK,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(big)))
		if w.Code != want {
			t.Errorf("%s: got %d, want %d", path, w.Code, want)
		}
	}
}
