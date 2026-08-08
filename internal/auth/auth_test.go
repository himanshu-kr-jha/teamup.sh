package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEAMUP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEAMUP_TEST_DATABASE_URL to run auth database tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedUser creates an org + owner user the way the setup wizard does.
func seedUser(t *testing.T, pool *pgxpool.Pool, email, password string) (orgID, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	subdomain := "authtest-" + uuid.New().String()[:8]
	if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE subdomain = $1`, subdomain); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	orgID = uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name, subdomain, created_at) VALUES ($1, 'Auth Test', $2, NOW())
	`, orgID, subdomain); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	})

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	userID = uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, org_id, name, email, password_hash, role, is_admin, created_at)
		VALUES ($1, $2, 'Test Admin', $3, $4, 'owner', true, NOW())
	`, userID, orgID, email, hash); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	roleID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO roles (id, org_id, name, permissions, is_system, created_at)
		VALUES ($1, $2, 'owner', '["*"]', true, NOW())
	`, roleID, orgID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, assigned_at) VALUES ($1, $2, NOW())
	`, userID, roleID); err != nil {
		t.Fatalf("insert user_role: %v", err)
	}
	return orgID, userID
}

func testService(pool *pgxpool.Pool) *Service {
	return NewService(pool, nil, Config{
		Issuer:          "teamup",
		Audience:        "teamup-api",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
	})
}

// GetUserByEmail was a stub returning ErrNotFound, so password login could
// never find the account the wizard had just created.
func TestGetUserByEmailFindsTheWizardCreatedAdmin(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "owner-" + uuid.New().String()[:8] + "@acme.test"
	orgID, userID := seedUser(t, pool, email, "correct-horse-battery")

	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if user.ID != userID || user.OrgID != orgID {
		t.Errorf("wrong user: got id=%s org=%s", user.ID, user.OrgID)
	}
	if user.Email != email {
		t.Errorf("email: got %q want %q", user.Email, email)
	}
	if !user.IsActive {
		t.Error("seeded user should be active")
	}
	if len(user.Roles) == 0 || user.Roles[0] != "owner" {
		t.Errorf("roles should include owner, got %v", user.Roles)
	}
	if user.PasswordHash == "" {
		t.Error("password hash must be loaded or VerifyPassword cannot work")
	}
}

func TestGetUserByEmailIsCaseInsensitive(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "mixed-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "correct-horse-battery")

	// Browsers happily send whatever the user typed.
	if _, err := svc.GetUserByEmail(ctx, upper(email)); err != nil {
		t.Errorf("login should not depend on email casing: %v", err)
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func TestGetUserByEmailUnknownReturnsNotFound(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)

	if _, err := svc.GetUserByEmail(context.Background(), "nobody@nowhere.test"); err == nil {
		t.Fatal("unknown email must not resolve to a user")
	}
}

func TestVerifyPassword(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "verify-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "correct-horse-battery")

	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if err := svc.VerifyPassword(user, "correct-horse-battery"); err != nil {
		t.Errorf("correct password must be accepted, got: %v", err)
	}
	if err := svc.VerifyPassword(user, "wrong-password-here"); err == nil {
		t.Error("wrong password must be rejected")
	}
}

func TestVerifyPasswordRejectsAccountWithoutPassword(t *testing.T) {
	svc := testService(nil)
	// OIDC-only accounts have no password_hash; they must not be loggable
	// with an empty password.
	if err := svc.VerifyPassword(&User{}, ""); err == nil {
		t.Fatal("an account with no password hash must not authenticate")
	}
}

// The refresh token is a 256-bit random string. It was hashed with bcrypt,
// which is salted, so "find the row by its hash" was impossible and the whole
// refresh path was stubbed out.
func TestRefreshTokenCanBeFoundByItsHash(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "rt-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")
	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	sessionID := uuid.New().String()
	rt, raw, err := svc.GenerateRefreshToken(ctx, user, sessionID, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	if rt.SessionID != sessionID {
		t.Errorf("refresh token should carry the session id it was issued for")
	}

	found, err := svc.findRefreshTokenByHash(ctx, raw)
	if err != nil {
		t.Fatalf("findRefreshTokenByHash: %v", err)
	}
	if found.ID != rt.ID || found.UserID != userID {
		t.Errorf("found the wrong token: %+v", found)
	}
	if found.RevokedAt != nil {
		t.Error("a fresh token should not be revoked")
	}
}

// The access token and its refresh token must share a session id, otherwise
// every access token references a session that has no row and is treated as
// revoked on the very next request.
func TestAccessAndRefreshTokensShareASession(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "sess-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "correct-horse-battery")
	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	sessionID := uuid.New().String()
	if _, err := svc.generateAndStoreRefreshToken(ctx, user, sessionID, "ua", "127.0.0.1"); err != nil {
		t.Fatalf("generateAndStoreRefreshToken: %v", err)
	}

	revoked, err := svc.isSessionRevoked(ctx, sessionID)
	if err != nil {
		t.Fatalf("isSessionRevoked: %v", err)
	}
	if revoked {
		t.Error("a session that was just issued must not read as revoked")
	}
}

func TestRevokeSessionMakesItRevoked(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "revoke-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "correct-horse-battery")
	user, _ := svc.GetUserByEmail(ctx, email)

	sessionID := uuid.New().String()
	if _, err := svc.generateAndStoreRefreshToken(ctx, user, sessionID, "ua", "127.0.0.1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := svc.RevokeSession(ctx, sessionID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	revoked, err := svc.isSessionRevoked(ctx, sessionID)
	if err != nil {
		t.Fatalf("isSessionRevoked: %v", err)
	}
	if !revoked {
		t.Error("session must read as revoked after RevokeSession")
	}
}

// An unknown session id has no row at all. Treating that as "not revoked"
// would let a forged or long-expired token through.
func TestUnknownSessionIsTreatedAsRevoked(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)

	revoked, err := svc.isSessionRevoked(context.Background(), uuid.New().String())
	if err != nil {
		t.Fatalf("isSessionRevoked: %v", err)
	}
	if !revoked {
		t.Error("a session with no stored token must be treated as revoked")
	}
}

func TestRefreshAccessTokenRotatesAndInvalidatesTheOldToken(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "rotate-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "correct-horse-battery")
	user, _ := svc.GetUserByEmail(ctx, email)

	sessionID := uuid.New().String()
	raw, err := svc.generateAndStoreRefreshToken(ctx, user, sessionID, "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	pair, err := svc.RefreshAccessToken(ctx, raw, "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("refresh must return a new token pair")
	}
	if pair.RefreshToken == raw {
		t.Error("refresh token must be rotated, not reused")
	}

	// Replaying the consumed token must fail.
	if _, err := svc.RefreshAccessToken(ctx, raw, "ua", "127.0.0.1"); err == nil {
		t.Error("a rotated-away refresh token must not work a second time")
	}
}

func TestRevokeAllUserTokens(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "revokeall-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")
	user, _ := svc.GetUserByEmail(ctx, email)

	s1, s2 := uuid.New().String(), uuid.New().String()
	if _, err := svc.generateAndStoreRefreshToken(ctx, user, s1, "ua", "127.0.0.1"); err != nil {
		t.Fatalf("issue 1: %v", err)
	}
	if _, err := svc.generateAndStoreRefreshToken(ctx, user, s2, "ua", "127.0.0.1"); err != nil {
		t.Fatalf("issue 2: %v", err)
	}

	if err := svc.RevokeAllUserTokens(ctx, userID); err != nil {
		t.Fatalf("RevokeAllUserTokens: %v", err)
	}
	for _, s := range []string{s1, s2} {
		revoked, err := svc.isSessionRevoked(ctx, s)
		if err != nil {
			t.Fatalf("isSessionRevoked: %v", err)
		}
		if !revoked {
			t.Errorf("session %s should be revoked", s)
		}
	}
}

func TestGetUserByIDLoadsRoles(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "byid-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")

	user, err := svc.getUser(ctx, userID)
	if err != nil {
		t.Fatalf("getUser: %v", err)
	}
	if user.Email != email {
		t.Errorf("email: got %q want %q", user.Email, email)
	}
	if len(user.Roles) == 0 {
		t.Error("roles should be loaded so permission checks work after a refresh")
	}
}

func TestLinkedIdentityRoundTrip(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "linked-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")

	li := &LinkedIdentity{
		UserID:      userID,
		Provider:    "google",
		ProviderID:  "google-subject-123",
		Email:       email,
		AccessToken: "at",
		IDToken:     "idt",
	}
	if err := svc.upsertLinkedIdentity(ctx, li); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	found, err := svc.getLinkedIdentity(ctx, "google", "google-subject-123")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if found.UserID != userID {
		t.Errorf("linked identity resolved to the wrong user: %s", found.UserID)
	}

	// Re-linking the same provider must update, not duplicate.
	li.AccessToken = "at2"
	if err := svc.upsertLinkedIdentity(ctx, li); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	found, err = svc.getLinkedIdentity(ctx, "google", "google-subject-123")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if found.AccessToken != "at2" {
		t.Errorf("upsert should update the stored token, got %q", found.AccessToken)
	}

	if err := svc.deleteLinkedIdentity(ctx, userID, "google"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.getLinkedIdentity(ctx, "google", "google-subject-123"); err == nil {
		t.Error("identity should be gone after unlink")
	}
}
