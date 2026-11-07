// Package testsupport provides the shared harness for tests that need a real
// PostgreSQL database.
//
// It exists because every package used to carry its own copy of the same
// connect-and-skip helper, and all of those copies shared one database. Tests
// in different packages then interfered with each other — seeding an
// organization in one package broke another package's assertion that only one
// organization existed — and the suite had to be pinned to serial execution to
// hide it.
//
// Sharing one database and truncating between tests does not fix that: with
// packages running in parallel, two test binaries truncating the same tables
// deadlock against each other and wipe each other's fixtures mid-test. So each
// test binary gets its own database, cloned from the migrated one named by
// TEAMUP_TEST_DATABASE_URL, and truncates only within itself.
package testsupport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvDatabaseURL names the environment variable holding the DSN of a migrated
// database used as the template. Tests skip when it is unset, so
// `go test ./...` still works on a machine with no PostgreSQL.
const EnvDatabaseURL = "TEAMUP_TEST_DATABASE_URL"

// tablesToClean lists every table carrying test-visible state. TRUNCATE
// CASCADE handles ordering itself, but naming them explicitly keeps schema
// migrations honest: a new table holding state has to be added here.
var tablesToClean = []string{
	"audit_logs", "linked_identities", "refresh_tokens", "signing_keys", "org_configs",
	"notifications", "push_subscriptions", "user_presence",
	"call_events", "call_sessions", "call_knock_requests", "call_participants", "calls", "standing_meeting_schedules", "standing_meetings", "calendar_blocks",
	"scrum_entries", "scrum_participants", "scrums",
	"sprint_tasks", "task_activities", "task_watchers", "task_attachments",
	"task_comments", "task_labels", "labels", "tasks", "sprints", "projects",
	"multipart_uploads", "attachments", "message_reactions", "messages",
	"channel_members", "channels", "team_members", "teams",
	"user_roles", "roles", "users", "organizations",
}

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

var unsafeDBChars = regexp.MustCompile(`[^a-z0-9_]`)

// callerPackage names the package under test, so each test binary gets its own
// database. Derived from the calling test file's directory.
func callerPackage() string {
	for skip := 2; skip < 12; skip++ {
		_, file, _, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		if strings.Contains(file, "/internal/testsupport/") {
			continue
		}
		name := filepath.Base(filepath.Dir(file))
		return unsafeDBChars.ReplaceAllString(strings.ToLower(name), "_")
	}
	return "unknown"
}

// DB returns a pool against this package's own database, emptied first, or
// skips when no test database is configured.
//
// Isolation comes from the per-package clone plus the truncation; tests inside
// one package therefore must not call t.Parallel().
func DB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	templateDSN := os.Getenv(EnvDatabaseURL)
	if templateDSN == "" {
		t.Skipf("set %s to run database tests (see `make test-db`)", EnvDatabaseURL)
	}

	pkg := callerPackage()
	poolOnce.Do(func() {
		pool, poolErr = openPackageDB(templateDSN, pkg)
	})
	if poolErr != nil {
		t.Fatalf("prepare test database for package %q: %v", pkg, poolErr)
	}

	Truncate(t, pool)
	return pool
}

// openPackageDB clones the migrated template into a database dedicated to this
// package and connects to it.
func openPackageDB(templateDSN, pkg string) (*pgxpool.Pool, error) {
	cfg, err := pgx.ParseConfig(templateDSN)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvDatabaseURL, err)
	}
	template := cfg.Database
	clone := template + "_" + pkg

	// The template cannot be cloned while anything is connected to it, so the
	// administrative work happens on the maintenance database instead.
	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(context.Background(), adminCfg)
	if err != nil {
		return nil, fmt.Errorf("connect maintenance database: %w", err)
	}
	defer admin.Close(context.Background())

	ctx := context.Background()
	// FORCE drops any connection left behind by an interrupted run.
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, clone)); err != nil {
		return nil, fmt.Errorf("drop %s: %w", clone, err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, clone, template)); err != nil {
		return nil, fmt.Errorf("clone %s from %s (is it migrated?): %w", clone, template, err)
	}

	// Build the pool from a parsed config rather than a connection string:
	// pgx's ConnString() returns the DSN it was parsed from, so overriding
	// .Database on a copy and handing back ConnString() silently reconnects to
	// the template — which is exactly the shared-database collision this
	// harness exists to prevent.
	poolCfg, err := pgxpool.ParseConfig(templateDSN)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	poolCfg.ConnConfig.Database = clone

	p, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", clone, err)
	}
	var connected string
	if err := p.QueryRow(ctx, `SELECT current_database()`).Scan(&connected); err != nil {
		p.Close()
		return nil, fmt.Errorf("ping %s: %w", clone, err)
	}
	if connected != clone {
		p.Close()
		return nil, fmt.Errorf("expected to be connected to %s but reached %s", clone, connected)
	}
	return p, nil
}

// Truncate empties every stateful table, returning the database to the state a
// freshly migrated instance is in.
func Truncate(t *testing.T, p *pgxpool.Pool) {
	t.Helper()

	stmt := "TRUNCATE TABLE " + strings.Join(tablesToClean, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := p.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}
}
