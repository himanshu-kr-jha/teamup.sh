package auth

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// browser is a real HTTP client with a real cookie jar against the real routes.
// The jar matters: like a browser it sends a cookie only to the paths the cookie
// names, and keeps a cookie unless it is expired on that same path. A test that
// hand-writes its Cookie headers cannot see either of the defects in ticket 50.
type browser struct {
	t      *testing.T
	base   *url.URL
	client *http.Client
	email  string
	// svc and pool are the server's own, for tests that need to reach behind
	// the HTTP surface, such as ageing a token or hooking the sign-out signal.
	svc  *Service
	pool *pgxpool.Pool
}

const logoutTestPassword = "Logout-pass-12345"

func newBrowser(t *testing.T) *browser {
	t.Helper()
	pool := testDB(t)
	svc, err := NewService(pool, nil, Config{
		Issuer: "teamup", Audience: "teamup-api",
		AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 24 * time.Hour,
		InsecureCookies: true,
	})
	if err != nil {
		t.Fatalf("build auth service: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api/auth"), svc)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	email := "logout-" + uuid.NewString() + "@example.test"
	seedUser(t, pool, email, logoutTestPassword)
	return &browser{t: t, base: base, client: &http.Client{Jar: jar}, email: email, svc: svc, pool: pool}
}

func (b *browser) url(path string) string { return b.base.String() + path }

func (b *browser) do(req *http.Request) *http.Response {
	b.t.Helper()
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res
}

func (b *browser) post(path, body string) *http.Response {
	b.t.Helper()
	req, err := http.NewRequest(http.MethodPost, b.url(path), strings.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return b.do(req)
}

func (b *browser) status(method, path string) int {
	b.t.Helper()
	req, err := http.NewRequest(method, b.url(path), nil)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.do(req).StatusCode
}

func (b *browser) login() *http.Response {
	b.t.Helper()
	res := b.post("/api/auth/login", `{"email":"`+b.email+`","password":"`+logoutTestPassword+`"}`)
	if res.StatusCode != http.StatusOK {
		b.t.Fatalf("login: %d", res.StatusCode)
	}
	return res
}

// cookie is what the jar would send to path, or "" when it would send nothing.
func (b *browser) cookie(path, name string) string {
	b.t.Helper()
	u, err := url.Parse(b.url(path))
	if err != nil {
		b.t.Fatal(err)
	}
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func (b *browser) setCookie(name, value, path string, maxAge int) {
	b.client.Jar.SetCookies(b.base, []*http.Cookie{{Name: name, Value: value, Path: path, MaxAge: maxAge}})
}

// replayRefresh presents a captured refresh token by hand, jar or no jar: the
// question is whether the server still honours it, not whether a polite browser
// happened to throw it away.
func (b *browser) replayRefresh(raw string) int {
	b.t.Helper()
	req, err := http.NewRequest(http.MethodPost, b.url("/api/auth/refresh"), nil)
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Cookie", "rt="+raw)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func (b *browser) bearerMe(token string) int {
	b.t.Helper()
	req, err := http.NewRequest(http.MethodGet, b.url("/api/auth/me"), nil)
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// The curl reproduction from ticket 50.
func TestLogoutEndsTheSession(t *testing.T) {
	b := newBrowser(t)
	b.login()
	access := b.cookie("/api/auth/me", "at")
	refresh := b.cookie("/api/auth/refresh", "rt")
	if access == "" || refresh == "" {
		t.Fatalf("login should leave both cookies, at=%q rt=%q", access, refresh)
	}

	if res := b.post("/api/auth/logout", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", res.StatusCode)
	}

	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusUnauthorized {
		t.Errorf("/me after logout: got %d, want 401", got)
	}
	if got := b.status(http.MethodPost, "/api/auth/refresh"); got != http.StatusUnauthorized {
		t.Errorf("/refresh after logout: got %d, want 401", got)
	}
	if got := b.cookie("/api/auth/refresh", "rt"); got != "" {
		t.Errorf("the browser still holds the refresh cookie after logout")
	}
	if got := b.replayRefresh(refresh); got != http.StatusUnauthorized {
		t.Errorf("the refresh token still works on the server after logout: got %d, want 401", got)
	}
	if got := b.bearerMe(access); got != http.StatusUnauthorized {
		t.Errorf("the access token still works on the server after logout: got %d, want 401", got)
	}
}

// Somebody who left the tab open past the access token's life can still log out.
func TestLogoutWithAnExpiredAccessTokenStillEndsTheSession(t *testing.T) {
	cases := map[string]func(b *browser){
		"access cookie gone, as the browser does at expiry": func(b *browser) { b.setCookie("at", "", "/", -1) },
		"access cookie no longer valid":                     func(b *browser) { b.setCookie("at", "not-a-token", "/", 60) },
	}
	for name, expire := range cases {
		t.Run(name, func(t *testing.T) {
			b := newBrowser(t)
			b.login()
			refresh := b.cookie("/api/auth/refresh", "rt")
			expire(b)

			if res := b.post("/api/auth/logout", ""); res.StatusCode != http.StatusOK {
				t.Fatalf("logout: %d", res.StatusCode)
			}
			if got := b.replayRefresh(refresh); got != http.StatusUnauthorized {
				t.Errorf("refresh after logout: got %d, want 401", got)
			}
			if got := b.cookie("/api/auth/refresh", "rt"); got != "" {
				t.Errorf("the browser still holds the refresh cookie after logout")
			}
		})
	}
}

func TestLogoutClearsTheRefreshCookieOnThePathItWasSetWith(t *testing.T) {
	b := newBrowser(t)
	setPath := ""
	for _, c := range b.login().Cookies() {
		if c.Name == "rt" && c.MaxAge > 0 {
			setPath = c.Path
		}
	}
	if setPath == "" {
		t.Fatal("login set no refresh cookie")
	}

	cleared := false
	for _, c := range b.post("/api/auth/logout", "").Cookies() {
		if c.Name == "rt" && c.Path == setPath && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("logout did not expire rt on %q", setPath)
	}
}

func TestLogoutAfterARotationEndsTheCurrentSession(t *testing.T) {
	b := newBrowser(t)
	b.login()
	first := b.cookie("/api/auth/refresh", "rt")
	if got := b.status(http.MethodPost, "/api/auth/refresh"); got != http.StatusOK {
		t.Fatalf("refresh: %d", got)
	}
	second := b.cookie("/api/auth/refresh", "rt")
	if first == second {
		t.Fatal("refresh should have rotated the token")
	}

	if res := b.post("/api/auth/logout", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	for name, raw := range map[string]string{"rotated-away token": first, "current token": second} {
		if got := b.replayRefresh(raw); got != http.StatusUnauthorized {
			t.Errorf("%s after logout: got %d, want 401", name, got)
		}
	}
}

func TestLogoutWithNoSessionAtAllIsStillFine(t *testing.T) {
	b := newBrowser(t)
	if res := b.post("/api/auth/logout", ""); res.StatusCode != http.StatusOK {
		t.Errorf("logout with no cookies: got %d, want 200", res.StatusCode)
	}
	b.setCookie("rt", "never-issued", "/api/auth", 60)
	if res := b.post("/api/auth/logout", ""); res.StatusCode != http.StatusOK {
		t.Errorf("logout with an unknown token: got %d, want 200", res.StatusCode)
	}
}

func TestLoggingInAgainAfterLogoutWorks(t *testing.T) {
	b := newBrowser(t)
	b.login()
	old := b.cookie("/api/auth/refresh", "rt")
	b.post("/api/auth/logout", "")

	b.login()

	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusOK {
		t.Errorf("/me after signing in again: got %d, want 200", got)
	}
	if got := b.status(http.MethodPost, "/api/auth/refresh"); got != http.StatusOK {
		t.Errorf("/refresh after signing in again: got %d, want 200", got)
	}
	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusOK {
		t.Errorf("/me after that refresh: got %d, want 200", got)
	}
	if got := b.replayRefresh(old); got != http.StatusUnauthorized {
		t.Errorf("the session that was logged out came back: got %d, want 401", got)
	}
}

// A stale access cookie left on the machine, for instance when the session was
// revoked from another device, must not get in the way of the login form.
func TestLoggingInOverARevokedSessionWorks(t *testing.T) {
	b := newBrowser(t)
	b.login()
	refresh := b.cookie("/api/auth/refresh", "rt")
	access := b.cookie("/api/auth/me", "at")
	b.post("/api/auth/logout", "")
	b.setCookie("at", access, "/", 60)
	b.setCookie("rt", refresh, "/api/auth", 60)

	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusUnauthorized {
		t.Fatalf("/me on a revoked session: got %d, want 401", got)
	}
	b.login()
	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusOK {
		t.Errorf("/me after signing in: got %d, want 200", got)
	}
	if got := b.status(http.MethodPost, "/api/auth/refresh"); got != http.StatusOK {
		t.Errorf("/refresh after signing in: got %d, want 200", got)
	}
}

// Browsers signed in before ticket 50 hold rt on the old, narrower path. Since a
// more specific path is sent first, leaving it there would shadow the new cookie
// and the old token would be the one the server reads, forever.
func TestARefreshCookieOnTheOldPathDoesNotShadowTheNewOne(t *testing.T) {
	t.Run("refreshing moves it", func(t *testing.T) {
		b := newBrowser(t)
		b.login()
		raw := b.cookie("/api/auth/refresh", "rt")
		b.setCookie("rt", "", "/api/auth", -1)
		b.setCookie("rt", raw, "/api/auth/refresh", 3600)

		for i := 0; i < 2; i++ {
			if got := b.status(http.MethodPost, "/api/auth/refresh"); got != http.StatusOK {
				t.Fatalf("refresh %d: got %d, want 200", i+1, got)
			}
		}
	})

	t.Run("logging in over a dead one", func(t *testing.T) {
		b := newBrowser(t)
		b.setCookie("rt", "dead-token", "/api/auth/refresh", 3600)
		b.login()
		if got := b.status(http.MethodPost, "/api/auth/refresh"); got != http.StatusOK {
			t.Errorf("refresh after login: got %d, want 200", got)
		}
	})

	t.Run("logging out revokes through the access token and clears it", func(t *testing.T) {
		b := newBrowser(t)
		b.login()
		raw := b.cookie("/api/auth/refresh", "rt")
		b.setCookie("rt", "", "/api/auth", -1)
		b.setCookie("rt", raw, "/api/auth/refresh", 3600)

		if res := b.post("/api/auth/logout", ""); res.StatusCode != http.StatusOK {
			t.Fatalf("logout: %d", res.StatusCode)
		}
		if got := b.cookie("/api/auth/refresh", "rt"); got != "" {
			t.Errorf("the old-path cookie survived logout")
		}
		if got := b.replayRefresh(raw); got != http.StatusUnauthorized {
			t.Errorf("refresh after logout: got %d, want 401", got)
		}
	})
}
