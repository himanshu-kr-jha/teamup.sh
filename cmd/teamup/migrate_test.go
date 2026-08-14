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
