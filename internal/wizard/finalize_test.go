package wizard

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// FinalizeSetup must reject bad data before it opens a transaction, so a
// Service with no database is enough to prove nothing gets written.
func TestFinalizeSetupRejectsIncompleteDataBeforeTouchingDB(t *testing.T) {
	svc := &Service{} // nil db: any DB access would panic

	err := svc.FinalizeSetup(context.Background(), Data{})
	if err == nil {
		t.Fatal("finalizing empty data must fail instead of creating a blank organization")
	}
	if !strings.Contains(err.Error(), "orgName") {
		t.Errorf("error should explain what is missing, got: %v", err)
	}
}

func TestFinalizeSetupRejectsShortPasswordBeforeTouchingDB(t *testing.T) {
	svc := &Service{}
	d := completeData()
	d.AdminPassword = "tooshort"

	if err := svc.FinalizeSetup(context.Background(), d); err == nil {
		t.Fatal("short admin password must be rejected")
	}
}

// testDB returns a pool against a disposable database, or skips.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEAMUP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEAMUP_TEST_DATABASE_URL to run wizard database tests")
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

func cleanupOrg(t *testing.T, pool *pgxpool.Pool, subdomain string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE subdomain = $1`, subdomain); err != nil {
		t.Fatalf("cleanup org %q: %v", subdomain, err)
	}
}

// FinalizeSetup enforces a single organization per instance, so these tests
// need the disposable test database to actually be empty. Other packages seed
// organizations of their own; run with -p 1 so they cannot overlap.
func resetOrgs(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM organizations`); err != nil {
		t.Fatalf("reset organizations: %v", err)
	}
}

func TestFinalizeSetupCreatesOrgAdminAndRoles(t *testing.T) {
	pool := testDB(t)
	svc := &Service{db: pool}
	ctx := context.Background()
	resetOrgs(t, pool)

	data := completeData()
	data.OrgSubdomain = "finalize-creates"
	cleanupOrg(t, pool, data.OrgSubdomain)
	t.Cleanup(func() { cleanupOrg(t, pool, data.OrgSubdomain) })

	if err := svc.FinalizeSetup(ctx, data); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	var orgID string
	var orgName string
	if err := pool.QueryRow(ctx,
		`SELECT id, name FROM organizations WHERE subdomain = $1`, data.OrgSubdomain,
	).Scan(&orgID, &orgName); err != nil {
		t.Fatalf("organization not created: %v", err)
	}
	if orgName != data.OrgName {
		t.Errorf("org name: got %q want %q", orgName, data.OrgName)
	}

	var adminName, adminEmail, adminRole, passwordHash string
	var isAdmin bool
	if err := pool.QueryRow(ctx,
		`SELECT name, email, role, is_admin, password_hash FROM users WHERE org_id = $1`, orgID,
	).Scan(&adminName, &adminEmail, &adminRole, &isAdmin, &passwordHash); err != nil {
		t.Fatalf("admin user not created: %v", err)
	}
	if adminName != data.AdminName || adminEmail != data.AdminEmail {
		t.Errorf("admin identity: got %q/%q want %q/%q", adminName, adminEmail, data.AdminName, data.AdminEmail)
	}
	if !isAdmin || adminRole != "owner" {
		t.Errorf("admin should be owner: role=%q is_admin=%v", adminRole, isAdmin)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(data.AdminPassword)); err != nil {
		t.Errorf("admin password hash does not verify: %v", err)
	}

	var roleCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM roles WHERE org_id = $1`, orgID).Scan(&roleCount); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if roleCount != 4 {
		t.Errorf("expected 4 default roles, got %d", roleCount)
	}

	// The admin must actually hold the owner role, or RBAC-gated features
	// are unreachable for the only account that exists.
	var linked int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE r.org_id = $1 AND r.name = 'owner'
	`, orgID).Scan(&linked); err != nil {
		t.Fatalf("count user_roles: %v", err)
	}
	if linked != 1 {
		t.Errorf("admin should be assigned the owner role, got %d assignments", linked)
	}

	for _, key := range []string{"oidc", "minio", "livekit", "vapid", "setup_complete"} {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM org_configs WHERE org_id = $1 AND key = $2`, orgID, key,
		).Scan(&n); err != nil {
			t.Fatalf("count config %s: %v", key, err)
		}
		if n != 1 {
			t.Errorf("config %q: got %d rows want 1", key, n)
		}
	}
}

// Spec 09 §7: setup must be re-runnable without exploding on unique constraints.
func TestFinalizeSetupIsIdempotent(t *testing.T) {
	pool := testDB(t)
	svc := &Service{db: pool}
	ctx := context.Background()
	resetOrgs(t, pool)

	data := completeData()
	data.OrgSubdomain = "finalize-idempotent"
	cleanupOrg(t, pool, data.OrgSubdomain)
	t.Cleanup(func() { cleanupOrg(t, pool, data.OrgSubdomain) })

	if err := svc.FinalizeSetup(ctx, data); err != nil {
		t.Fatalf("first finalize: %v", err)
	}

	data.OrgName = "Acme Renamed"
	if err := svc.FinalizeSetup(ctx, data); err != nil {
		t.Fatalf("second finalize should succeed, got: %v", err)
	}

	var orgs int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM organizations WHERE subdomain = $1`, data.OrgSubdomain,
	).Scan(&orgs); err != nil {
		t.Fatalf("count orgs: %v", err)
	}
	if orgs != 1 {
		t.Errorf("re-running setup must not duplicate the org, got %d", orgs)
	}

	var orgID, name string
	if err := pool.QueryRow(ctx,
		`SELECT id, name FROM organizations WHERE subdomain = $1`, data.OrgSubdomain,
	).Scan(&orgID, &name); err != nil {
		t.Fatalf("load org: %v", err)
	}
	if name != "Acme Renamed" {
		t.Errorf("re-running setup should update the org name, got %q", name)
	}

	var users, roles int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE org_id = $1`, orgID).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 1 {
		t.Errorf("re-running setup must not duplicate the admin, got %d", users)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM roles WHERE org_id = $1`, orgID).Scan(&roles); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if roles != 4 {
		t.Errorf("re-running setup must not duplicate roles, got %d", roles)
	}
}

// SetupGuardMiddleware checks completion outside the transaction, so two
// finalizes can race past it. A second, different organization must still be
// refused inside the transaction — otherwise an unauthenticated caller racing
// the operator lands a permanent owner account.
func TestFinalizeSetupRefusesASecondOrganization(t *testing.T) {
	pool := testDB(t)
	svc := &Service{db: pool}
	ctx := context.Background()
	resetOrgs(t, pool)

	first := completeData()
	first.OrgSubdomain = "race-first"
	cleanupOrg(t, pool, first.OrgSubdomain)
	t.Cleanup(func() { cleanupOrg(t, pool, first.OrgSubdomain) })

	second := completeData()
	second.OrgSubdomain = "race-second"
	second.AdminEmail = "intruder@evil.test"
	cleanupOrg(t, pool, second.OrgSubdomain)
	t.Cleanup(func() { cleanupOrg(t, pool, second.OrgSubdomain) })

	if err := svc.FinalizeSetup(ctx, first); err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	if err := svc.FinalizeSetup(ctx, second); err == nil {
		t.Fatal("a second organization must be refused on a provisioned instance")
	}

	var orgs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&orgs); err != nil {
		t.Fatalf("count orgs: %v", err)
	}
	if orgs != 1 {
		t.Errorf("expected exactly one organization, got %d", orgs)
	}
}

func TestIsSetupCompleteReflectsOrganizations(t *testing.T) {
	pool := testDB(t)
	svc := &Service{db: pool}
	ctx := context.Background()
	resetOrgs(t, pool)

	data := completeData()
	data.OrgSubdomain = "setup-complete-check"
	cleanupOrg(t, pool, data.OrgSubdomain)
	t.Cleanup(func() { cleanupOrg(t, pool, data.OrgSubdomain) })

	if err := svc.FinalizeSetup(ctx, data); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	complete, err := svc.IsSetupComplete(ctx)
	if err != nil {
		t.Fatalf("IsSetupComplete: %v", err)
	}
	if !complete {
		t.Error("setup should report complete once an organization exists")
	}
}
