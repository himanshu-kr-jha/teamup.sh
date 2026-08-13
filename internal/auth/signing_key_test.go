package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// deploymentConfig is the configuration every replica of one deployment is
// given: the same settings, from the same environment, against the same
// database. Nothing in it names a signing key, because nothing in the
// application ever set one.
func deploymentConfig() Config {
	return Config{
		Issuer:          "teamup",
		Audience:        "teamup-api",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
	}
}

// buildService constructs a Service the way cmd/teamup does — one call, one
// database pool, one configuration. Two calls stand for two replicas, or for
// the same replica either side of a restart.
func buildService(t *testing.T, pool *pgxpool.Pool, cfg Config) *Service {
	t.Helper()
	svc, err := NewService(pool, nil, cfg)
	if err != nil {
		t.Fatalf("build auth service: %v", err)
	}
	return svc
}

// liveToken mints an access token with a session behind it, so validation has
// something to find when it checks the session has not been revoked.
func liveToken(t *testing.T, svc *Service, user *User) string {
	t.Helper()
	ctx := context.Background()
	sessionID := uuid.New().String()
	if _, _, err := svc.GenerateRefreshToken(ctx, user, sessionID, "test", "127.0.0.1"); err != nil {
		t.Fatalf("refresh token: %v", err)
	}
	token, err := svc.GenerateAccessToken(ctx, user, sessionID)
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	return token
}

func seedAccount(t *testing.T, pool *pgxpool.Pool, svc *Service, prefix string) *User {
	t.Helper()
	email := prefix + "-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "correct-horse-battery")
	user, err := svc.GetUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("load seeded user: %v", err)
	}
	return user
}

// The headline defect: the signing key was minted in NewService and never
// stored, so two replicas of one deployment each signed with a key the other
// had never seen. Every request a load balancer sent to the other pod came
// back 401.
func TestATokenFromOneReplicaValidatesOnAnother(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	replicaA := buildService(t, pool, deploymentConfig())
	replicaB := buildService(t, pool, deploymentConfig())

	user := seedAccount(t, pool, replicaA, "replica")
	token := liveToken(t, replicaA, user)

	if _, err := replicaA.ValidateAccessToken(ctx, token); err != nil {
		t.Fatalf("the replica that issued the token must accept it: %v", err)
	}
	claims, err := replicaB.ValidateAccessToken(ctx, token)
	if err != nil {
		t.Fatalf("a second replica of the same deployment must accept it: %v", err)
	}
	if claims.UserID != user.ID.String() {
		t.Errorf("token resolved to the wrong user: %s", claims.UserID)
	}
}

// A restart used to invalidate every access token in the field, because the
// process came back with a new key.
func TestTheSigningKeySurvivesARestart(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	before := buildService(t, pool, deploymentConfig())
	user := seedAccount(t, pool, before, "restart")
	token := liveToken(t, before, user)

	// The process goes away and comes back with the same configuration.
	after := buildService(t, pool, deploymentConfig())

	if _, err := after.ValidateAccessToken(ctx, token); err != nil {
		t.Fatalf("a token issued before the restart must still be accepted: %v", err)
	}
	if !before.cfg.PublicKey.Equal(after.cfg.PublicKey) {
		t.Error("the restarted process should have loaded the stored key, not made a new one")
	}
}

// N replicas of a new deployment boot at the same moment against an empty
// database. Exactly one key must end up stored, and every replica — including
// one that starts later — must be using it.
func TestConcurrentFirstBootAgreesOnOneKey(t *testing.T) {
	pool := testDB(t)

	const replicas = 8
	keys := make([]*rsa.PublicKey, replicas)
	errs := make([]error, replicas)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			svc, err := NewService(pool, nil, deploymentConfig())
			if err != nil {
				errs[i] = err
				return
			}
			keys[i] = svc.cfg.PublicKey
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d failed to boot: %v", i, err)
		}
	}
	for i, k := range keys {
		if !keys[0].Equal(k) {
			t.Fatalf("replica %d booted with a different signing key than replica 0", i)
		}
	}

	// A replica that joins afterwards has to agree with the ones that raced:
	// two rows persisted by a lost race would only show up here.
	later := buildService(t, pool, deploymentConfig())
	if !keys[0].Equal(later.cfg.PublicKey) {
		t.Error("a replica starting after the storm loaded a different key")
	}
}

// Persisting the key must not turn into accepting anything. A token signed by
// a key this deployment does not use stays worthless.
func TestATokenSignedWithAnotherKeyIsRejected(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	svc := buildService(t, pool, deploymentConfig())
	user := seedAccount(t, pool, svc, "forged")

	// A live session, so the only thing wrong with the token is its signature.
	sessionID := uuid.New().String()
	if _, _, err := svc.GenerateRefreshToken(ctx, user, sessionID, "test", "127.0.0.1"); err != nil {
		t.Fatalf("refresh token: %v", err)
	}

	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate attacker key: %v", err)
	}
	forged, err := jwt.NewWithClaims(jwt.SigningMethodRS256, &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    "teamup",
			Audience:  []string{"teamup-api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		UserID:    user.ID.String(),
		OrgID:     user.OrgID.String(),
		Email:     user.Email,
		SessionID: sessionID,
	}).SignedString(attacker)
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}

	if _, err := svc.ValidateAccessToken(ctx, forged); err == nil {
		t.Fatal("a token signed with a key this deployment does not hold must be rejected")
	}

	// Nor may an unsigned token walk in.
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID:    user.ID.String(),
		SessionID: sessionID,
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none-alg token: %v", err)
	}
	if _, err := svc.ValidateAccessToken(ctx, unsigned); err == nil {
		t.Fatal("an unsigned token must be rejected")
	}
}

// ── the refresh stampede ──────────────────────────────────────────────────

// Every client refreshes at once after a restart or a rolling deploy, and a
// PWA refetches several queries on window focus. Rotation revoked the
// presented token before issuing its replacement, so the first caller won and
// every other one was signed out.
func TestConcurrentRefreshDoesNotSignTheUserOut(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	svc := buildService(t, pool, deploymentConfig())
	user := seedAccount(t, pool, svc, "stampede")

	raw, err := svc.generateAndStoreRefreshToken(ctx, user, uuid.New().String(), "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("issue refresh token: %v", err)
	}

	const callers = 10
	pairs := make([]*TokenPair, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			pairs[i], errs[i] = svc.RefreshAccessToken(ctx, raw, "ua", "127.0.0.1")
		}(i)
	}
	close(start)
	wg.Wait()

	rotated := 0
	for i := range pairs {
		if errs[i] != nil {
			t.Errorf("caller %d was signed out: %v", i, errs[i])
			continue
		}
		if _, err := svc.ValidateAccessToken(ctx, pairs[i].AccessToken); err != nil {
			t.Errorf("caller %d got an access token that does not validate: %v", i, err)
		}
		if pairs[i].RefreshToken != "" {
			rotated++
		}
	}
	if rotated != 1 {
		t.Errorf("the refresh token should be rotated exactly once, got %d rotations", rotated)
	}
}

// The same stampede as the ticket reproduces it: through the middleware, with
// a dead access token and a live refresh cookie, which is the state every
// client is in after a restart.
func TestConcurrentRequestsThroughTheRefreshFallbackAllSucceed(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	svc := buildService(t, pool, deploymentConfig())
	user := seedAccount(t, pool, svc, "middleware")

	raw, err := svc.generateAndStoreRefreshToken(ctx, user, uuid.New().String(), "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("issue refresh token: %v", err)
	}

	r := mustChangeRouter(svc)
	const callers = 10
	codes := make([]int, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
			req.Header.Set("Cookie", "at=garbage.token.value; rt="+raw)
			w := httptest.NewRecorder()
			<-start
			r.ServeHTTP(w, req)
			codes[i] = w.Code
		}(i)
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("caller %d got %d; a burst of requests from one client must not sign it out", i, code)
		}
	}
}

// The grace window is narrow and deliberate: outside it, a token that has been
// rotated away is dead, and presenting it never yields a new refresh token.
func TestARotatedTokenIsDeadOnceTheGraceWindowPasses(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	svc := buildService(t, pool, deploymentConfig())
	user := seedAccount(t, pool, svc, "grace")

	raw, err := svc.generateAndStoreRefreshToken(ctx, user, uuid.New().String(), "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("issue refresh token: %v", err)
	}
	if _, err := svc.RefreshAccessToken(ctx, raw, "ua", "127.0.0.1"); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Within the window the old token still gets its holder an access token,
	// but never another refresh token.
	pair, err := svc.RefreshAccessToken(ctx, raw, "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("a caller racing the rotation must not be signed out: %v", err)
	}
	if pair.RefreshToken != "" {
		t.Error("the grace path must not hand out a second refresh token")
	}

	// Age the rotation past the window.
	if _, err := pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = NOW() - INTERVAL '1 hour'
		WHERE token_hash = $1
	`, hashRefreshToken(raw)); err != nil {
		t.Fatalf("age the rotated token: %v", err)
	}
	if _, err := svc.RefreshAccessToken(ctx, raw, "ua", "127.0.0.1"); err == nil {
		t.Error("a token rotated away an hour ago must be refused")
	}
}

// ── TEAMUP_AUTH_SECRET ────────────────────────────────────────────────────

// storedSigningKey reads the row as it actually sits in the database.
func storedSigningKey(t *testing.T, pool *pgxpool.Pool) (wrapping string, material []byte) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT wrapping, private_key FROM signing_keys ORDER BY created_at LIMIT 1`).
		Scan(&wrapping, &material)
	if err != nil {
		t.Fatalf("read stored signing key: %v", err)
	}
	return wrapping, material
}

// TEAMUP_AUTH_SECRET was parsed, documented and shipped in the production
// compose file — and read by nothing at all. It now wraps the stored key, so a
// database dump on its own does not carry the ability to mint tokens.
func TestTheAuthSecretEncryptsTheStoredKey(t *testing.T) {
	pool := testDB(t)

	cfg := deploymentConfig()
	cfg.SigningKeySecret = "a-shared-secret-that-operators-think-pins-the-key"
	svc := buildService(t, pool, cfg)

	wrapping, material := storedSigningKey(t, pool)
	if wrapping == wrapNone {
		t.Fatal("the key was stored in the clear even though a secret was configured")
	}
	if _, err := x509.ParsePKCS8PrivateKey(material); err == nil {
		t.Fatal("what is stored is still a usable private key")
	}

	// The same secret gets the same key back.
	again := buildService(t, pool, cfg)
	if !svc.cfg.PublicKey.Equal(again.cfg.PublicKey) {
		t.Error("restarting with the same secret must recover the same key")
	}

	// A different secret must fail loudly rather than quietly minting a new
	// key and signing everybody out.
	wrong := deploymentConfig()
	wrong.SigningKeySecret = "somebody-rotated-the-secret-without-thinking"
	if _, err := NewService(pool, nil, wrong); err == nil {
		t.Error("booting with the wrong secret must fail, not carry on with a new key")
	}
	// So must dropping it altogether.
	if _, err := NewService(pool, nil, deploymentConfig()); err == nil {
		t.Error("booting with the secret removed must fail rather than reissue")
	}
}

// Setting the secret on a deployment that started without one should protect
// the key already in use, not demand a new one — otherwise turning the setting
// on signs everybody out, and operators learn not to turn it on.
func TestSettingTheAuthSecretLaterEncryptsTheKeyInUse(t *testing.T) {
	pool := testDB(t)

	plain := buildService(t, pool, deploymentConfig())
	if wrapping, _ := storedSigningKey(t, pool); wrapping != wrapNone {
		t.Fatalf("without a secret the key should be stored as-is, got %q", wrapping)
	}

	cfg := deploymentConfig()
	cfg.SigningKeySecret = "set-after-the-fact"
	encrypted := buildService(t, pool, cfg)

	if !plain.cfg.PublicKey.Equal(encrypted.cfg.PublicKey) {
		t.Error("turning the secret on must keep the key that is already in use")
	}
	if wrapping, _ := storedSigningKey(t, pool); wrapping == wrapNone {
		t.Error("the stored key should have been encrypted on the way past")
	}
}
