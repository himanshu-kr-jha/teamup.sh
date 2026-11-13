package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// send is browser.do without throwing the body away, for tests that read it.
func (b *browser) send(method, path, body string) (int, string) {
	b.t.Helper()
	req, err := http.NewRequest(method, b.url(path), strings.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// The OIDC sign-in was a stub: its callback made up a user with random ids and
// handed out a real session for it, and the back-channel logout read a token
// it never checked. None of it may be reachable until it is built for real.
func TestUnfinishedSignInRoutesAreNotServed(t *testing.T) {
	b := newBrowser(t)
	b.login()

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/login/google"},
		{http.MethodGet, "/api/auth/callback/google"},
		{http.MethodGet, "/api/auth/callback/google?state=&code=x"},
		{http.MethodPost, "/api/auth/backchannel-logout"},
		{http.MethodPost, "/api/auth/link/google"},
		{http.MethodDelete, "/api/auth/link/google"},
	} {
		if got := b.status(route.method, route.path); got != http.StatusNotFound {
			t.Errorf("%s %s: got %d, want 404", route.method, route.path, got)
		}
	}
}

// The tokens live in HttpOnly cookies so page script cannot read them. A body
// that repeats them undoes that, so neither sign-in nor refresh may carry one.
func TestNoTokenIsEverInAResponseBody(t *testing.T) {
	b := newBrowser(t)

	code, body := b.send(http.MethodPost, "/api/auth/login",
		`{"email":"`+b.email+`","password":"`+logoutTestPassword+`"}`)
	if code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	assertNoTokens(t, "login", body, b.cookie("/api/auth/me", "at"), b.cookie("/api/auth/refresh", "rt"))

	code, body = b.send(http.MethodPost, "/api/auth/refresh", "")
	if code != http.StatusOK {
		t.Fatalf("refresh: %d", code)
	}
	assertNoTokens(t, "refresh", body, b.cookie("/api/auth/me", "at"), b.cookie("/api/auth/refresh", "rt"))
	var answer map[string]any
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("refresh answer is not JSON: %v", err)
	}
	if _, ok := answer["expiresIn"]; !ok {
		t.Errorf("refresh should still say when the access token runs out, got %s", body)
	}
}

func assertNoTokens(t *testing.T, what, body string, cookies ...string) {
	t.Helper()
	for _, field := range []string{"access_token", "refresh_token", "accessToken", "refreshToken"} {
		if strings.Contains(body, field) {
			t.Errorf("%s answer names %q: %s", what, field, body)
		}
	}
	for _, c := range cookies {
		if c != "" && strings.Contains(body, c) {
			t.Errorf("%s answer repeats a token cookie", what)
		}
	}
}

// A refresh token is spent once it is rotated. One presented again after the
// grace window can only be a copy, so somebody else holds this person's
// session: every session they have ends, and their live connections close.
func TestReplayingASpentRefreshTokenEndsEverySession(t *testing.T) {
	b := newBrowser(t)
	b.login()
	stolen := b.cookie("/api/auth/refresh", "rt")

	if res := b.post("/api/auth/refresh", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("rotate: %d", res.StatusCode)
	}
	if b.cookie("/api/auth/refresh", "rt") == stolen {
		t.Fatal("refresh should have rotated the token")
	}
	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusOK {
		t.Fatalf("the rotated session should work before the replay: %d", got)
	}

	var ended []string
	b.svc.OnSessionsEnded(func(userID string) { ended = append(ended, userID) })
	if _, err := b.pool.Exec(context.Background(), `
		UPDATE refresh_tokens SET revoked_at = NOW() - INTERVAL '1 hour'
		WHERE token_hash = $1`, hashRefreshToken(stolen)); err != nil {
		t.Fatalf("age the spent token: %v", err)
	}

	if got := b.replayRefresh(stolen); got != http.StatusUnauthorized {
		t.Fatalf("replaying a spent token: got %d, want 401", got)
	}
	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusUnauthorized {
		t.Errorf("the legitimate session must end too, since either side may be the thief: got %d", got)
	}
	if got := b.status(http.MethodPost, "/api/auth/refresh"); got != http.StatusUnauthorized {
		t.Errorf("the current refresh token must be revoked as well: got %d", got)
	}
	var live int
	if err := b.pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM refresh_tokens r JOIN users u ON u.id = r.user_id
		WHERE LOWER(u.email) = LOWER($1) AND r.revoked_at IS NULL`, b.email).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Errorf("%d sessions still live after a replay", live)
	}
	if len(ended) != 1 {
		t.Errorf("live connections should be told once, got %v", ended)
	}
}

// Within the grace window a replay is a burst from one client, not theft, and
// must not sign anybody out.
func TestARotationRaceIsNotTakenForTheft(t *testing.T) {
	b := newBrowser(t)
	b.login()
	raced := b.cookie("/api/auth/refresh", "rt")
	if res := b.post("/api/auth/refresh", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("rotate: %d", res.StatusCode)
	}
	if got := b.replayRefresh(raced); got != http.StatusOK {
		t.Fatalf("a racing caller inside the window: got %d, want 200", got)
	}
	if got := b.status(http.MethodGet, "/api/auth/me"); got != http.StatusOK {
		t.Errorf("a race must leave the session alone: got %d", got)
	}
}

// Only RS256 is ever issued, so only RS256 is accepted: a parser that takes
// any RSA variant accepts tokens this server never minted the way it mints them.
func TestOnlyRS256TokensAreAccepted(t *testing.T) {
	pool := testDB(t)
	svc := buildService(t, pool, deploymentConfig())
	user := seedAccount(t, pool, svc, "alg")
	ctx := context.Background()

	sessionID := uuid.NewString()
	if _, _, err := svc.GenerateRefreshToken(ctx, user, sessionID, "ua", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    "teamup",
			Audience:  []string{"teamup-api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		UserID: user.ID.String(), OrgID: user.OrgID.String(), SessionID: sessionID,
	}
	for _, method := range []jwt.SigningMethod{jwt.SigningMethodRS384, jwt.SigningMethodRS512, jwt.SigningMethodPS256} {
		token, err := jwt.NewWithClaims(method, claims).SignedString(svc.cfg.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ValidateAccessToken(ctx, token); err == nil {
			t.Errorf("a %s token was accepted", method.Alg())
		}
	}
	good, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(svc.cfg.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ValidateAccessToken(ctx, good); err != nil {
		t.Errorf("an RS256 token was refused: %v", err)
	}
}

// Every token this server mints names it as issuer and itself as audience, so
// a token minted for something else under the same key is not one of ours.
func TestATokenForAnotherIssuerOrAudienceIsRejected(t *testing.T) {
	pool := testDB(t)
	svc := buildService(t, pool, deploymentConfig())
	user := seedAccount(t, pool, svc, "aud")

	for name, cfg := range map[string]Config{
		"issuer":   {Issuer: "somebody-else", Audience: "teamup-api"},
		"audience": {Issuer: "teamup", Audience: "somebody-else"},
	} {
		cfg.PrivateKey = svc.cfg.PrivateKey
		cfg.AccessTokenTTL, cfg.RefreshTokenTTL = time.Minute, time.Hour
		other := buildService(t, pool, cfg)
		if _, err := svc.ValidateAccessToken(context.Background(), liveToken(t, other, user)); err == nil {
			t.Errorf("a token with another %s was accepted", name)
		}
	}
}

// Guessing the current password on the change-password form must cost the
// same as guessing it on the sign-in form, or a stolen session becomes an
// unlimited password oracle for the account.
func TestWrongCurrentPasswordsLockTheAccount(t *testing.T) {
	b := newBrowser(t)
	b.login()

	wrong := `{"currentPassword":"not-it-at-all","newPassword":"A-brand-new-pass-123"}`
	for i := 0; i < loginFailuresAllowed; i++ {
		if code, _ := b.send(http.MethodPost, "/api/auth/password", wrong); code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d: got %d, want 401", i+1, code)
		}
	}

	right := `{"currentPassword":"` + logoutTestPassword + `","newPassword":"A-brand-new-pass-123"}`
	if code, _ := b.send(http.MethodPost, "/api/auth/password", right); code != http.StatusTooManyRequests {
		t.Errorf("after %d wrong guesses even the right one must wait: got %d, want 429", loginFailuresAllowed, code)
	}
	if code, _ := b.send(http.MethodPost, "/api/auth/login",
		`{"email":"`+b.email+`","password":"`+logoutTestPassword+`"}`); code != http.StatusTooManyRequests {
		t.Errorf("signing in to the locked account: got %d, want 429", code)
	}
}
