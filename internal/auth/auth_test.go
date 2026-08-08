package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/teamup/teamup/internal/testsupport"
)

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testsupport.DB(t)
}

// seedUser creates an org + owner user the way the setup wizard does, with the
// given email and password.
func seedUser(t *testing.T, pool *pgxpool.Pool, email, password string) (orgID, userID uuid.UUID) {
	t.Helper()

	org := testsupport.SeedOrg(t, pool)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE users SET email = $2, password_hash = $3 WHERE id = $1
	`, org.OwnerID, email, hash); err != nil {
		t.Fatalf("set credentials: %v", err)
	}
	return org.ID, org.OwnerID
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

// Changing a password is the one credential operation a self-hosted admin
// genuinely needs: the wizard sets the initial password and there was no way
// to rotate it.
func TestChangePassword(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "rotate-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")

	if err := svc.ChangePassword(ctx, userID, "wrong-current-one", "brand-new-password"); err == nil {
		t.Fatal("the current password must be verified before changing it")
	}

	if err := svc.ChangePassword(ctx, userID, "correct-horse-battery", "brand-new-password"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := svc.VerifyPassword(user, "brand-new-password"); err != nil {
		t.Errorf("the new password should authenticate: %v", err)
	}
	if err := svc.VerifyPassword(user, "correct-horse-battery"); err == nil {
		t.Error("the old password must stop working")
	}
}

func TestChangePasswordEnforcesLength(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "short-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")

	if err := svc.ChangePassword(ctx, userID, "correct-horse-battery", "short"); err == nil {
		t.Fatal("a password under the minimum length must be rejected")
	}
}

// Rotating the password must not leave old sessions usable.
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "revoke-pw-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")
	user, _ := svc.GetUserByEmail(ctx, email)

	sessionID := uuid.New().String()
	if _, err := svc.generateAndStoreRefreshToken(ctx, user, sessionID, "ua", "127.0.0.1"); err != nil {
		t.Fatalf("issue session: %v", err)
	}

	if err := svc.ChangePassword(ctx, userID, "correct-horse-battery", "brand-new-password"); err != nil {
		t.Fatalf("change: %v", err)
	}

	revoked, err := svc.isSessionRevoked(ctx, sessionID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !revoked {
		t.Error("existing sessions must be revoked when the password changes")
	}
}

func TestListSessionsReturnsActiveOnes(t *testing.T) {
	pool := testDB(t)
	svc := testService(pool)
	ctx := context.Background()

	email := "sessions-" + uuid.New().String()[:8] + "@acme.test"
	_, userID := seedUser(t, pool, email, "correct-horse-battery")
	user, _ := svc.GetUserByEmail(ctx, email)

	s1 := uuid.New().String()
	if _, err := svc.generateAndStoreRefreshToken(ctx, user, s1, "Firefox on Linux", "127.0.0.1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	sessions, err := svc.ListSessions(ctx, userID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one active session, got %d", len(sessions))
	}
	if sessions[0].UserAgent != "Firefox on Linux" {
		t.Errorf("session should carry the device it was issued to: %+v", sessions[0])
	}

	if err := svc.RevokeSession(ctx, s1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	sessions, _ = svc.ListSessions(ctx, userID)
	if len(sessions) != 0 {
		t.Errorf("a revoked session should not be listed, got %d", len(sessions))
	}
}
