package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The migrations directory, relative to this package. Tests run with their own
// package directory as the working directory, so the command's own
// `file://migrations` default does not resolve here.
const testMigrationsSource = "file://../../migrations"

const testMigrationsDir = "../../migrations"

// EnvTestDatabaseURL names the DSN whose *server* these tests borrow. They do
// not touch the database it names: every test creates and drops an empty one
// of its own, because `migrate` has to be exercised from nothing.
const envTestDatabaseURL = "TEAMUP_TEST_DATABASE_URL"

// noVersion is what golang-migrate records when no migration is applied. It is
// -1, not 0: version 0 is a version that has never existed, and reporting it is
// how the old `migrate version` hid a half-rolled-back database.
const noVersion = int64(-1)

// --- harness -----------------------------------------------------------

// migrateFixture is a throwaway database plus the wiring to run the command
// against it.
type migrateFixture struct {
	t   *testing.T
	dsn string
	// name of the database, which down-all asks the operator to type back.
	dbName string
}

var unsafeDBName = regexp.MustCompile(`[^a-z0-9_]`)

// newMigrateFixture creates an empty database named after the test. Empty is
// the point: testsupport.DB hands out a *migrated* clone, which is the one
// starting state a migration test cannot use.
func newMigrateFixture(t *testing.T) *migrateFixture {
	t.Helper()

	templateDSN := os.Getenv(envTestDatabaseURL)
	if templateDSN == "" {
		t.Skipf("set %s to run migration tests", envTestDatabaseURL)
	}

	cfg, err := pgx.ParseConfig(templateDSN)
	if err != nil {
		t.Fatalf("parse %s: %v", envTestDatabaseURL, err)
	}

	name := "migratefix_" + unsafeDBName.ReplaceAllString(strings.ToLower(t.Name()), "_")
	if len(name) > 60 {
		name = name[:60]
	}

	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	ctx := context.Background()
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	defer admin.Close(ctx)

	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupCfg := cfg.Copy()
		cleanupCfg.Database = "postgres"
		conn, err := pgx.ConnectConfig(context.Background(), cleanupCfg)
		if err != nil {
			t.Logf("cleanup: connect: %v", err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
			t.Logf("cleanup: drop %s: %v", name, err)
		}
	})

	dsnCfg := cfg.Copy()
	dsnCfg.Database = name
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		dsnCfg.User, dsnCfg.Password, dsnCfg.Host, dsnCfg.Port, name)

	return &migrateFixture{t: t, dsn: dsn, dbName: name}
}

// run drives the command exactly as main() does, capturing what the operator
// would have seen.
func (f *migrateFixture) run(args ...string) (string, error) {
	f.t.Helper()
	return f.runWith(migrateEnv{}, args...)
}

// runWith is run() with parts of the environment overridden — TEAMUP_ENV, or
// what the operator types at a prompt.
func (f *migrateFixture) runWith(e migrateEnv, args ...string) (string, error) {
	f.t.Helper()
	var out bytes.Buffer
	e.DatabaseURL = f.dsn
	e.Out = &out
	if e.MigrationsPath == "" {
		e.MigrationsPath = testMigrationsSource
	}
	if e.In == nil {
		e.In = strings.NewReader("")
	}
	err := executeMigrate(e, args)
	return out.String(), err
}

// mustRun fails the test if the command did not succeed.
func (f *migrateFixture) mustRun(args ...string) string {
	f.t.Helper()
	out, err := f.run(args...)
	if err != nil {
		f.t.Fatalf("teamup migrate %s: %v\noutput:\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (f *migrateFixture) conn() *pgx.Conn {
	f.t.Helper()
	c, err := pgx.Connect(context.Background(), f.dsn)
	if err != nil {
		f.t.Fatalf("connect %s: %v", f.dbName, err)
	}
	f.t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

// recorded reads golang-migrate's own bookkeeping. Assertions go against this
// rather than against what the command printed, so a command that lies about
// where it left the database still fails.
//
// The driver stores "no migrations applied" as an empty table rather than as a
// -1 row — it only writes the -1 row when that state is also dirty, which is
// the state the bug report ended in. Both read back here as version -1.
// present is false only when the table itself is absent, i.e. nothing has ever
// migrated this database.
func (f *migrateFixture) recorded() (version int64, dirty bool, present bool) {
	f.t.Helper()
	c := f.conn()
	ctx := context.Background()

	var exists bool
	if err := c.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		f.t.Fatalf("look for schema_migrations: %v", err)
	}
	if !exists {
		return noVersion, false, false
	}
	rows, err := c.Query(ctx, `SELECT version, dirty FROM schema_migrations`)
	if err != nil {
		f.t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return noVersion, false, true
	}
	if err := rows.Scan(&version, &dirty); err != nil {
		f.t.Fatalf("scan schema_migrations: %v", err)
	}
	return version, dirty, true
}

// assertAt fails unless the database is recorded at exactly this version and
// clean.
// downTo rolls back one migration at a time until the database is at this version,
// however many later ones exist.
func (f *migrateFixture) downTo(want int64) {
	f.t.Helper()
	for i := 0; i < 100; i++ {
		version, _, _ := f.recorded()
		if version <= want {
			f.assertAt(want)
			return
		}
		f.mustRun("down")
	}
	f.t.Fatalf("never reached version %d", want)
}

func (f *migrateFixture) assertAt(want int64) {
	f.t.Helper()
	got, dirty, present := f.recorded()
	if !present {
		f.t.Fatalf("no version recorded, want %d", want)
	}
	if got != want {
		f.t.Fatalf("recorded version: got %d, want %d", got, want)
	}
	if dirty {
		f.t.Fatalf("database left dirty at version %d", got)
	}
}

// applicationTables lists the tables the migrations own — everything in the
// public schema except golang-migrate's own bookkeeping.
func (f *migrateFixture) applicationTables() []string {
	f.t.Helper()
	rows, err := f.conn().Query(context.Background(),
		`SELECT tablename FROM pg_tables
		  WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
		  ORDER BY tablename`)
	if err != nil {
		f.t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			f.t.Fatalf("scan table name: %v", err)
		}
		names = append(names, n)
	}
	return names
}

// schemaSnapshot is a stable text rendering of everything the migrations
// build: columns, indexes, constraints and extensions. Two snapshots comparing
// equal is what "the schema came back" means.
func (f *migrateFixture) schemaSnapshot() string {
	f.t.Helper()
	queries := []struct {
		label string
		sql   string
	}{
		{"column", `SELECT c.table_name || '.' || c.column_name || ' ' || c.data_type ||
		                   ' null=' || c.is_nullable || ' default=' || coalesce(c.column_default, '-')
		            FROM information_schema.columns c
		            JOIN information_schema.tables t
		              ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		            WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		              AND c.table_name <> 'schema_migrations'`},
		{"index", `SELECT indexname || ' = ' || indexdef FROM pg_indexes
		           WHERE schemaname = 'public' AND tablename <> 'schema_migrations'`},
		{"constraint", `SELECT conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
		                FROM pg_constraint
		                WHERE connamespace = 'public'::regnamespace
		                  AND conrelid::regclass::text <> 'schema_migrations'`},
		{"extension", `SELECT extname FROM pg_extension`},
	}

	c := f.conn()
	var lines []string
	for _, q := range queries {
		rows, err := c.Query(context.Background(), q.sql)
		if err != nil {
			f.t.Fatalf("snapshot %s: %v", q.label, err)
		}
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				rows.Close()
				f.t.Fatalf("snapshot %s scan: %v", q.label, err)
			}
			got = append(got, q.label+" "+s)
		}
		rows.Close()
		sort.Strings(got)
		lines = append(lines, got...)
	}
	return strings.Join(lines, "\n")
}

// headVersion is the highest migration on disk, read from the directory rather
// than hardcoded so adding a migration does not silently stop being covered.
func headVersion(t *testing.T) int64 {
	t.Helper()
	entries, err := os.ReadDir(testMigrationsDir)
	if err != nil {
		t.Fatalf("read %s: %v", testMigrationsDir, err)
	}
	var head int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		digits := name[:strings.IndexByte(name, '_')]
		v, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			t.Fatalf("migration %q has no numeric version prefix", name)
		}
		if v > head {
			head = v
		}
	}
	if head == 0 {
		t.Fatalf("no migrations found in %s", testMigrationsDir)
	}
	return head
}

func requireContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// --- one step down ------------------------------------------------------

// `migrate down` is named after one migration and the Makefile calls it
// "Rollback last migration", but it called m.Down(), which unwinds the entire
// schema. On this repository it did not even manage that: it failed partway
// through 0001 and left the database recorded at -1 and dirty, at which point
// every later command refused and the database had to be dropped.
func TestMigrateDownStepsBackExactlyOneMigration(t *testing.T) {
	f := newMigrateFixture(t)
	head := headVersion(t)

	f.mustRun("up")
	f.assertAt(head)
	atHead := f.schemaSnapshot()

	out := f.mustRun("down")
	f.assertAt(head - 1)

	// Silence here is how somebody runs it twice by accident.
	requireContains(t, out, strconv.FormatInt(head, 10), strconv.FormatInt(head-1, 10))

	if tables := f.applicationTables(); len(tables) == 0 {
		t.Fatalf("one step down emptied the schema; it rolled back everything")
	}

	f.mustRun("up")
	f.assertAt(head)
	if got := f.schemaSnapshot(); got != atHead {
		t.Errorf("schema did not return to head after down+up")
	}
}

// Somebody who left and came back has two rows with one address, which the
// constraint 0016 replaced cannot hold. Rolling back has to cope with that
// rather than leave the operator stuck on a failed migration.
func TestEmailMigrationRollsBackPastARejoinedMember(t *testing.T) {
	f := newMigrateFixture(t)
	f.mustRun("up")
	for v := headVersion(t); v > 16; v-- {
		f.mustRun("down")
	}

	ctx := context.Background()
	conn := f.conn()
	if _, err := conn.Exec(ctx, `
		WITH o AS (
			INSERT INTO organizations (name, subdomain) VALUES ('Rejoin', 'rejoin') RETURNING id
		)
		INSERT INTO users (org_id, email, password_hash, name, deleted_at)
		SELECT id, 'a@example.test', 'x', v.name, v.deleted_at::timestamptz
		FROM o, (VALUES ('left', '2024-03-01T00:00:00Z'), ('left again', '2024-04-01T00:00:00Z'), ('back', NULL)) AS v(name, deleted_at)
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f.mustRun("down")
	f.assertAt(15)

	var email string
	if err := conn.QueryRow(ctx, `SELECT email FROM users WHERE name = 'back'`).Scan(&email); err != nil {
		t.Fatalf("read: %v", err)
	}
	if email != "a@example.test" {
		t.Errorf("the person still here lost their address: %q", email)
	}
}

// Tracking counts a task by completed_at, which tasks finished before it was
// kept reliably do not have. 0015 backfills them, and rolling back must not undo
// that: the value is true either way, and nothing else could restore it.
func TestScrumsMigrationBackfillsCompletedAtOnFinishedTasks(t *testing.T) {
	const scrumsVersion = 15

	f := newMigrateFixture(t)
	f.mustRun("up")
	for v := headVersion(t); v >= scrumsVersion; v-- {
		f.mustRun("down")
	}
	f.assertAt(scrumsVersion - 1)

	ctx := context.Background()
	conn := f.conn()

	if _, err := conn.Exec(ctx, `
		WITH o AS (
			INSERT INTO organizations (name, subdomain) VALUES ('Backfill', 'backfill') RETURNING id
		), u AS (
			INSERT INTO users (org_id, email, password_hash, name)
			SELECT id, 'a@example.test', 'x', 'A' FROM o RETURNING id, org_id
		)
		INSERT INTO tasks (org_id, reporter_id, title, status, updated_at, completed_at)
		SELECT org_id, id, v.title, v.status, v.updated_at::timestamptz, v.completed_at::timestamptz
		FROM u, (VALUES
			('finished long ago', 'done', '2024-03-01T00:00:00Z', NULL),
			('finished and tracked', 'done', '2024-03-01T00:00:00Z', '2024-02-01T00:00:00Z'),
			('still open', 'todo', '2024-03-01T00:00:00Z', NULL)
		) AS v(title, status, updated_at, completed_at)
	`); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}

	check := func(when string) {
		t.Helper()
		for title, want := range map[string]string{
			"finished long ago":    "2024-03-01",
			"finished and tracked": "2024-02-01",
			"still open":           "",
		} {
			var got string
			if err := conn.QueryRow(ctx,
				`SELECT COALESCE(to_char(completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), '') FROM tasks WHERE title = $1`,
				title).Scan(&got); err != nil {
				t.Fatalf("load %q: %v", title, err)
			}
			if got != want {
				t.Errorf("%s: %q has completed_at %q, want %q", when, title, got, want)
			}
		}
	}

	f.mustRun("up")
	check("after up")
	f.mustRun("down")
	check("after down")
}

// Scrums already saved on the live database have to read the same after the Added
// to columns arrive, and rolling back must not leave an added-to task looking like
// somebody's own work.
func TestScrumAddedToMigrationKeepsSavedScrumsReadable(t *testing.T) {
	const addedToVersion = 21

	f := newMigrateFixture(t)
	f.mustRun("up")
	for v := headVersion(t); v >= addedToVersion; v-- {
		f.mustRun("down")
	}
	f.assertAt(addedToVersion - 1)

	ctx := context.Background()
	conn := f.conn()
	if _, err := conn.Exec(ctx, `
		WITH o AS (
			INSERT INTO organizations (name, subdomain) VALUES ('Saved', 'saved') RETURNING id
		), s AS (
			INSERT INTO scrums (org_id, scope, held_by_name, done_since)
			SELECT id, 'org', 'Holder', NOW() FROM o RETURNING id
		), p AS (
			INSERT INTO scrum_participants (scrum_id, user_id, name, pending, ongoing, blocked, overdue, completed)
			SELECT id, '00000000-0000-0000-0000-000000000001', 'Alice', 1, 2, 3, 4, 5 FROM s
		)
		INSERT INTO scrum_entries (scrum_id, user_id, task_id, title, status, priority, overdue)
		SELECT id, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'Kept', 'todo', 'high', false FROM s
	`); err != nil {
		t.Fatalf("seed scrum: %v", err)
	}

	f.mustRun("up")
	var pending, addedTo int
	var title string
	var flagged bool
	if err := conn.QueryRow(ctx, `
		SELECT p.pending, p.added_to, e.title, e.added_to
		FROM scrum_participants p JOIN scrum_entries e USING (scrum_id, user_id)
	`).Scan(&pending, &addedTo, &title, &flagged); err != nil {
		t.Fatalf("read old scrum after up: %v", err)
	}
	if pending != 1 || addedTo != 0 || title != "Kept" || flagged {
		t.Errorf("an old scrum reads as saved, with nothing added to, got %d %d %q %v", pending, addedTo, title, flagged)
	}

	// Now one that does record an added-to task.
	if _, err := conn.Exec(ctx, `
		INSERT INTO scrum_entries (scrum_id, user_id, task_id, title, status, priority, overdue, added_to)
		SELECT scrum_id, user_id, '00000000-0000-0000-0000-000000000003', 'Followed', 'todo', 'high', false, true
		FROM scrum_participants
	`); err != nil {
		t.Fatalf("seed added-to entry: %v", err)
	}
	f.downTo(addedToVersion - 1)
	var entries int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM scrum_entries WHERE title = 'Kept'`).Scan(&entries); err != nil {
		t.Fatalf("read after down: %v", err)
	}
	if entries != 1 {
		t.Errorf("the saved work survives rolling back, got %d", entries)
	}
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM scrum_entries WHERE title = 'Followed'`).Scan(&entries); err != nil {
		t.Fatalf("read after down: %v", err)
	}
	if entries != 0 {
		t.Error("without the column an added-to row would read as their own work")
	}
}

// A meeting nobody has held means nothing without its columns, while calls that
// really happened are history and stay.
func TestRollingBackStandingMeetingsDropsMeetingsNeverHeldAndKeepsCalls(t *testing.T) {
	const standingVersion = 19

	f := newMigrateFixture(t)
	f.mustRun("up")
	for v := headVersion(t); v > standingVersion; v-- {
		f.mustRun("down")
	}
	f.assertAt(standingVersion)

	ctx := context.Background()
	conn := f.conn()
	if _, err := conn.Exec(ctx, `
		WITH o AS (
			INSERT INTO organizations (name, subdomain) VALUES ('Calls', 'calls') RETURNING id
		), u AS (
			INSERT INTO users (org_id, email, password_hash, name)
			SELECT id, 'a@example.test', 'x', 'A' FROM o RETURNING id, org_id
		)
		INSERT INTO calls (org_id, room_name, title, status, created_by, scheduled_start, scheduled_end)
		SELECT org_id, v.title, v.title, v.status, id, v.at::timestamptz, v.at::timestamptz
		FROM u, (VALUES
			('never held', 'scheduled', '2026-01-01T00:00:00Z'),
			('called off', 'cancelled', '2026-01-01T00:00:00Z'),
			('held', 'ended', '2026-01-01T00:00:00Z'),
			('plain call', 'ended', NULL)
		) AS v(title, status, at)
	`); err != nil {
		t.Fatalf("seed calls: %v", err)
	}

	f.mustRun("down")
	f.assertAt(standingVersion - 1)

	rows, err := conn.Query(ctx, `SELECT title FROM calls ORDER BY title`)
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	titles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	if strings.Join(titles, ",") != "held,plain call" {
		t.Errorf("want held,plain call, got %v", titles)
	}
}

// A second `down` has to move one more step, not become a full teardown once
// the first one has run.
func TestMigrateDownTwiceStepsBackTwice(t *testing.T) {
	f := newMigrateFixture(t)
	head := headVersion(t)

	f.mustRun("up")
	f.mustRun("down")
	f.assertAt(head - 1)
	f.mustRun("down")
	f.assertAt(head - 2)
}

// --- every migration in the chain --------------------------------------

// A broken down migration anywhere in the chain is the same trap as a broken
// 0001: it is only found by the operator who steps onto it. This walks the
// whole chain down one migration at a time, requiring each step to move the
// version by exactly one and to leave the database clean, and then builds it
// all back up and requires the schema to be the one it started from.
func TestEveryDownMigrationStepsBackCleanly(t *testing.T) {
	f := newMigrateFixture(t)
	head := headVersion(t)

	f.mustRun("up")
	f.assertAt(head)
	atHead := f.schemaSnapshot()

	for v := head; v >= 1; v-- {
		out, err := f.run("down")
		if err != nil {
			t.Fatalf("down from version %d: %v\noutput:\n%s", v, err, out)
		}
		if v > 1 {
			f.assertAt(v - 1)
			continue
		}
		got, dirty, present := f.recorded()
		if !present || got != noVersion || dirty {
			t.Fatalf("after rolling back 0001: recorded (%d, dirty=%v, present=%v), want (-1, false, true)", got, dirty, present)
		}
	}

	// The proof that 0001's down actually works: nothing of the schema is left.
	if tables := f.applicationTables(); len(tables) != 0 {
		t.Fatalf("rolling back every migration left tables behind: %v", tables)
	}

	f.mustRun("up")
	f.assertAt(head)
	if got := f.schemaSnapshot(); got != atHead {
		t.Errorf("schema differs after a full round trip through every migration")
	}
}

// --- down-all -----------------------------------------------------------

// Rolling everything back is still wanted sometimes, so it keeps a command —
// under a name nobody types by accident, and behind a confirmation.
func TestDownAllRollsBackEverythingAndUpRestoresIt(t *testing.T) {
	f := newMigrateFixture(t)
	head := headVersion(t)

	f.mustRun("up")
	atHead := f.schemaSnapshot()

	out, err := f.runWith(migrateEnv{In: strings.NewReader(f.dbName + "\n")}, "down-all")
	if err != nil {
		t.Fatalf("down-all: %v\noutput:\n%s", err, out)
	}

	// It has to say what is about to be lost before it does it.
	requireContains(t, out, f.dbName, strconv.FormatInt(head, 10))

	got, dirty, present := f.recorded()
	if !present || got != noVersion || dirty {
		t.Fatalf("after down-all: recorded (%d, dirty=%v, present=%v), want (-1, false, true)", got, dirty, present)
	}
	if tables := f.applicationTables(); len(tables) != 0 {
		t.Fatalf("down-all left tables behind: %v", tables)
	}

	f.mustRun("up")
	f.assertAt(head)
	if now := f.schemaSnapshot(); now != atHead {
		t.Errorf("schema did not come back after down-all + up")
	}
}

// The whole point of the rename is that the destructive one cannot be reached
// by a script, a habit or a typo.
func TestDownAllRefusesWithoutTheRightConfirmation(t *testing.T) {
	tests := []struct {
		name    string
		typed   string
		wantErr string
	}{
		{"nothing typed", "", "not confirmed"},
		{"a cheerful yes", "yes\n", "not confirmed"},
		{"someone else's database", "teamup_production\n", "not confirmed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newMigrateFixture(t)
			head := headVersion(t)
			f.mustRun("up")

			out, err := f.runWith(migrateEnv{In: strings.NewReader(tc.typed)}, "down-all")
			if err == nil {
				t.Fatalf("down-all went ahead unconfirmed\noutput:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
			f.assertAt(head)
			if len(f.applicationTables()) == 0 {
				t.Fatalf("an unconfirmed down-all dropped the schema anyway")
			}
		})
	}
}

// TEAMUP_ENV exists; a command that drops every table in the database is the
// one place it has to be consulted.
func TestDownAllRefusesInProductionEvenWhenConfirmed(t *testing.T) {
	f := newMigrateFixture(t)
	head := headVersion(t)
	f.mustRun("up")

	out, err := f.runWith(migrateEnv{
		Environment: "production",
		In:          strings.NewReader(f.dbName + "\n"),
	}, "down-all")
	if err == nil {
		t.Fatalf("down-all ran in production\noutput:\n%s", out)
	}
	requireContains(t, err.Error(), "production", "TEAMUP_ENV")

	f.assertAt(head)
	if len(f.applicationTables()) == 0 {
		t.Fatalf("down-all dropped the schema in production")
	}
}

// --- dirty state --------------------------------------------------------

// A migration that fails partway leaves golang-migrate marked dirty, and every
// later command refuses with "Dirty database version N. Fix and force
// version." — which does not say what to fix, or what to force it to. An
// operator meeting this for the first time drops the database.
func TestCommandsExplainADirtyDatabaseInsteadOfRepeatingTheLibrary(t *testing.T) {
	head := headVersion(t)

	for _, action := range []string{"up", "down", "down-all"} {
		t.Run(action, func(t *testing.T) {
			f := newMigrateFixture(t)
			f.mustRun("up")
			if _, err := f.conn().Exec(context.Background(),
				`UPDATE schema_migrations SET dirty = true`); err != nil {
				t.Fatalf("mark dirty: %v", err)
			}

			_, err := f.run(action)
			if err == nil {
				t.Fatalf("%s ran against a dirty database", action)
			}
			msg := err.Error()
			requireContains(t, msg,
				"dirty",                     // what happened
				strconv.FormatInt(head, 10), // which migration
				"teamup migrate force",      // how to get out
			)
		})
	}
}

// `version` and `force` are the two commands an operator reaches for when the
// database is dirty, so they are the two that must still work.
func TestVersionAndForceStillWorkOnADirtyDatabase(t *testing.T) {
	f := newMigrateFixture(t)
	head := headVersion(t)
	f.mustRun("up")
	if _, err := f.conn().Exec(context.Background(), `UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}

	out := f.mustRun("version")
	requireContains(t, out, strconv.FormatInt(head, 10), "dirty")

	f.mustRun("force", strconv.FormatInt(head, 10))
	f.assertAt(head)
}

// The reported failure left the database at version -1 and dirty, and
// `migrate version` printed "Version: 0, Dirty: false" — a version that has
// never existed, and the opposite of the truth about the dirty flag. That
// reading is what sends an operator to `DROP DATABASE`.
func TestVersionTellsTheTruthAtTheBottomOfTheChain(t *testing.T) {
	f := newMigrateFixture(t)
	f.mustRun("up")
	if _, err := f.conn().Exec(context.Background(),
		`UPDATE schema_migrations SET version = -1, dirty = true`); err != nil {
		t.Fatalf("plant the reported state: %v", err)
	}

	out := f.mustRun("version")
	if strings.Contains(out, "Version: 0") {
		t.Errorf("reported version 0 for a database at -1:\n%s", out)
	}
	requireContains(t, out, "dirty")
}

// A database with no migrations applied is not "version 0" either.
func TestVersionOnAnUntouchedDatabase(t *testing.T) {
	f := newMigrateFixture(t)
	out := f.mustRun("version")
	if strings.Contains(out, "Version: 0") {
		t.Errorf("reported version 0 for a database with no migrations:\n%s", out)
	}
	requireContains(t, out, "no migrations")
}

// `migrate force -1` is the documented escape hatch out of the state this
// ticket is about, and it could not be reached: `fmt.Sscanf` into a uint left
// the value at zero and its error was discarded, so both `force -- -1` and
// `force banana` reported "Forced version to 0" and recorded version 0 — a
// version with no migration file, which wedges `up` completely.
func TestForceRefusesAVersionItCannotParse(t *testing.T) {
	// "0" is the value the old parser fell back to on every one of these, and
	// the worst of them: version 0 has no migration file, so `up` afterwards
	// fails with "no migration found for version 0" and the database is stuck
	// harder than the dirty state it was supposed to escape.
	for _, arg := range []string{"banana", "3.5", "-2", "0", ""} {
		t.Run("force_"+arg, func(t *testing.T) {
			f := newMigrateFixture(t)
			head := headVersion(t)
			f.mustRun("up")

			if _, err := f.run("force", arg); err == nil {
				t.Fatalf("force %q was accepted", arg)
			}
			f.assertAt(head)
		})
	}
}

// Forcing back to "no migrations applied" is the way out of a failed 0001, so
// it has to be expressible without fighting the flag parser over a leading
// minus sign.
func TestForceCanRecordThatNoMigrationIsApplied(t *testing.T) {
	f := newMigrateFixture(t)
	f.mustRun("up")

	f.mustRun("force", "nil")
	got, dirty, present := f.recorded()
	if !present || got != noVersion || dirty {
		t.Fatalf("force nil: recorded (%d, dirty=%v, present=%v), want (-1, false, true)", got, dirty, present)
	}
}

// --- up -----------------------------------------------------------------

// `up` printed "Migrations applied successfully" whether it applied fourteen
// migrations or none, and never said what version it arrived at.
func TestUpReportsWhereItStartedAndWhereItArrived(t *testing.T) {
	f := newMigrateFixture(t)
	head := headVersion(t)

	out := f.mustRun("up")
	requireContains(t, out, strconv.FormatInt(head, 10))

	again := f.mustRun("up")
	if !strings.Contains(again, "no migrations to apply") && !strings.Contains(again, "already at") {
		t.Errorf("a second up claimed to have done something:\n%s", again)
	}
	f.assertAt(head)
}

// --- create -------------------------------------------------------------

// `migrate create` stamped a timestamp version onto a repository numbered
// 0001..0014, wrote into whatever `./migrations` meant from the caller's
// working directory, and accepted a name that cannot be a migration.
func TestCreateContinuesTheSequentialNumbering(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"0001_initial_schema", "0002_must_change_password"} {
		for _, suffix := range []string{".up.sql", ".down.sql"} {
			if err := os.WriteFile(filepath.Join(dir, name+suffix), []byte("SELECT 1;\n"), 0o644); err != nil {
				t.Fatalf("seed %s: %v", name, err)
			}
		}
	}

	var out bytes.Buffer
	err := executeMigrate(migrateEnv{
		MigrationsPath: "file://" + dir,
		Out:            &out,
		In:             strings.NewReader(""),
	}, []string{"create", "add", "widgets"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, want := range []string{"0003_add_widgets.up.sql", "0003_add_widgets.down.sql"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("expected %s: %v", want, err)
		}
	}
	requireContains(t, out.String(), "0003_add_widgets")
}

func TestCreateRejectsNamesThatCannotBeMigrations(t *testing.T) {
	for _, name := range []string{"../escape", "Has Spaces And Caps!", ""} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			dir := t.TempDir()
			args := []string{"create"}
			if name != "" {
				args = append(args, name)
			}
			var out bytes.Buffer
			err := executeMigrate(migrateEnv{
				MigrationsPath: "file://" + dir,
				Out:            &out,
				In:             strings.NewReader(""),
			}, args)
			if err == nil {
				t.Fatalf("create %q was accepted", name)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Errorf("rejected name still wrote files: %v", entries)
			}
		})
	}
}

// Calls whose last person closed a laptop stayed active for ever, and everybody ever
// rung for one was busy on the calendar up to this minute.
func TestClosingStaleCallsLeavesLiveOnesMeetingsAndADownAlone(t *testing.T) {
	const closingVersion = 22
	f := newMigrateFixture(t)
	f.mustRun("up")
	f.downTo(closingVersion - 1)
	conn := f.conn()
	ctx := context.Background()

	if _, err := conn.Exec(ctx, `
		WITH o AS (INSERT INTO organizations (name, subdomain) VALUES ('Stale', 'stale') RETURNING id),
		u AS (INSERT INTO users (id, org_id, name, email, role, is_active)
		      SELECT '00000000-0000-0000-0000-0000000000a1', id, 'Ann', 'ann@stale.test', 'member', true FROM o RETURNING id, org_id)
		INSERT INTO calls (id, org_id, room_name, call_type, status, created_by, started_at, scheduled_start, scheduled_end)
		SELECT v.id, u.org_id, v.room, 'video', 'active', u.id, v.started, v.sched_start, v.sched_end FROM u, (VALUES
			('00000000-0000-0000-0000-0000000000c1'::uuid, 'old',     NOW() - interval '40 days', NULL::timestamptz, NULL::timestamptz),
			('00000000-0000-0000-0000-0000000000c2'::uuid, 'never',   NOW() - interval '40 days', NULL, NULL),
			('00000000-0000-0000-0000-0000000000c3'::uuid, 'today',   NOW() - interval '1 hour', NULL, NULL),
			('00000000-0000-0000-0000-0000000000c4'::uuid, 'meeting', NOW() - interval '40 days', NOW() - interval '40 days', NOW() - interval '39 days')
		) AS v(id, room, started, sched_start, sched_end)
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		WITH j AS (SELECT NOW() - interval '39 days' AS at)
		INSERT INTO call_sessions (call_id, user_id, participant_sid, joined_at)
		SELECT '00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000a1', 'PA_1', at FROM j
	`); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	f.mustRun("up")

	statusOf := func(id string) (status string, ended *time.Time) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT status, ended_at FROM calls WHERE id = $1`, id).Scan(&status, &ended); err != nil {
			t.Fatal(err)
		}
		return status, ended
	}
	status, ended := statusOf("00000000-0000-0000-0000-0000000000c1")
	if status != "ended" || ended == nil || time.Since(*ended) < 38*24*time.Hour {
		t.Errorf("over as of the last thing known about it, got %s %v", status, ended)
	}
	if status, ended = statusOf("00000000-0000-0000-0000-0000000000c2"); status != "ended" || ended == nil {
		t.Errorf("nobody ever came, got %s %v", status, ended)
	}
	if status, _ = statusOf("00000000-0000-0000-0000-0000000000c3"); status != "active" {
		t.Errorf("a call today may be going on, got %s", status)
	}
	if status, _ = statusOf("00000000-0000-0000-0000-0000000000c4"); status != "active" {
		t.Errorf("a meeting is the sweep's to decide, got %s", status)
	}
	var open int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM call_sessions WHERE left_at IS NULL`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Errorf("the session closed with the call, %d still open", open)
	}
	var trail int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM call_events WHERE type = 'left'`).Scan(&trail); err != nil {
		t.Fatal(err)
	}
	if trail != 1 {
		t.Errorf("the trail says they left, got %d", trail)
	}
	f.downTo(closingVersion - 1)
	if status, _ = statusOf("00000000-0000-0000-0000-0000000000c1"); status != "ended" {
		t.Errorf("rolling back does not resurrect a call, got %s", status)
	}
}

// Who added whom, and what schedule ran when, are extra knowledge. Rolling them back
// keeps the watchers and the meetings.
func TestRollingBackAddedByAndSchedulesKeepsTheRows(t *testing.T) {
	const addedByVersion = 23
	f := newMigrateFixture(t)
	f.mustRun("up")
	f.downTo(addedByVersion - 1)
	conn := f.conn()
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `
		WITH o AS (INSERT INTO organizations (name, subdomain) VALUES ('Keep', 'keep') RETURNING id),
		u AS (INSERT INTO users (id, org_id, name, email, role, is_active)
		      SELECT '00000000-0000-0000-0000-0000000000a2', id, 'Ann', 'ann@keep.test', 'member', true FROM o RETURNING id, org_id),
		t AS (INSERT INTO tasks (id, org_id, title, reporter_id)
		      SELECT '00000000-0000-0000-0000-0000000000d1', org_id, 'Old', id FROM u RETURNING id),
		w AS (INSERT INTO task_watchers (task_id, user_id) SELECT t.id, u.id FROM t, u)
		INSERT INTO standing_meetings (org_id, name, time_of_day, time_zone, weekdays)
		SELECT org_id, 'Scrum', '09:00', 'UTC', '{1,2}' FROM u
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f.mustRun("up")
	// Up goes to the newest migration, whichever that is by now.
	f.assertAt(headVersion(t))
	var addedBy *string
	if err := conn.QueryRow(ctx, `SELECT added_by::text FROM task_watchers`).Scan(&addedBy); err != nil {
		t.Fatal(err)
	}
	if addedBy == nil || *addedBy != "00000000-0000-0000-0000-0000000000a2" {
		t.Errorf("no notification was ever raised for it, so they followed it themself, got %v", addedBy)
	}
	var versions int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM standing_meeting_schedules`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Errorf("an existing meeting starts with the schedule it has, got %d", versions)
	}

	f.downTo(addedByVersion - 1)
	var watchers, meetings int
	if err := conn.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM task_watchers), (SELECT COUNT(*) FROM standing_meetings)`).Scan(&watchers, &meetings); err != nil {
		t.Fatal(err)
	}
	if watchers != 1 || meetings != 1 {
		t.Errorf("both are kept, got %d watchers and %d meetings", watchers, meetings)
	}
}

// Forwarded messages and profile pictures arrive in one migration. Going up
// leaves every message unforwarded; going back keeps the messages and puts
// anybody with an uploaded picture back to initials, because what pointed at
// the picture is gone.
func TestForwardedAndAvatarsMigrationIsReversible(t *testing.T) {
	const version = 25
	f := newMigrateFixture(t)
	f.mustRun("up")
	f.downTo(version - 1)
	conn := f.conn()
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `
		WITH o AS (INSERT INTO organizations (name, subdomain) VALUES ('Fwd', 'fwd') RETURNING id),
		u AS (INSERT INTO users (id, org_id, name, email, role, is_active)
		      SELECT '00000000-0000-0000-0000-0000000000b1', id, 'Bea', 'bea@fwd.test', 'member', true FROM o RETURNING id, org_id),
		c AS (INSERT INTO channels (id, org_id, name, type, created_by)
		      SELECT '00000000-0000-0000-0000-0000000000c1', org_id, 'general', 'public', id FROM u RETURNING id)
		INSERT INTO messages (org_id, channel_id, user_id, content)
		SELECT u.org_id, c.id, u.id, 'before' FROM u, c
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f.mustRun("up")
	var forwarded bool
	if err := conn.QueryRow(ctx, `SELECT forwarded FROM messages`).Scan(&forwarded); err != nil || forwarded {
		t.Fatalf("an existing message is not forwarded: %v %v", forwarded, err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO user_avatars (user_id, content_type, data) VALUES ('00000000-0000-0000-0000-0000000000b1', 'image/png', '\x89');
		UPDATE users SET avatar_url = '/api/users/00000000-0000-0000-0000-0000000000b1/avatar?v=1';
	`); err != nil {
		t.Fatal(err)
	}

	f.downTo(version - 1)
	var messages int
	var avatar *string
	if err := conn.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM messages), (SELECT avatar_url FROM users)`).Scan(&messages, &avatar); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || avatar != nil {
		t.Errorf("messages kept, picture back to initials: %d %v", messages, avatar)
	}
}
