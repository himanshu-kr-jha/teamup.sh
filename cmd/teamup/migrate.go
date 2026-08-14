package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"
)

// noMigrationsApplied is the version golang-migrate records for a database no
// migration has touched, and the one it passes through while rolling back the
// first migration. It is -1, not 0. The old `migrate version` discarded the
// ErrNilVersion that reports it and printed the zero value instead, so a
// database stuck at -1 and dirty read back as "Version: 0, Dirty: false" —
// a version that has never existed, and the opposite of the truth.
const noMigrationsApplied = int64(database.NilVersion)

// migrateEnv is everything `teamup migrate` needs from outside the process,
// gathered into one value so the command can be driven against a throwaway
// database by a test instead of only by main(). Nothing below reads the
// environment, the working directory or the real stdin/stdout directly.
type migrateEnv struct {
	DatabaseURL    string
	MigrationsPath string    // golang-migrate source URL, e.g. file://migrations
	Environment    string    // TEAMUP_ENV; consulted only by down-all
	Out            io.Writer // where progress and prompts go
	In             io.Reader // where a confirmation is read from
}

// migrateConfig is the environment the migrate command reads. It stays
// separate from Config for the same reason DatabaseConfig does: a pre-deploy
// job or an init container legitimately has the database URL and none of the
// service credentials, and must still be able to migrate.
type migrateConfig struct {
	DatabaseConfig
	// Gates `down-all`, and nothing else. Mirrors Config.Environment, which
	// this command cannot reach without also demanding every service
	// credential Config requires.
	Environment string `env:"TEAMUP_ENV" envDefault:"development"`
}

// runMigrate adapts the cobra command to migrateEnv.
func runMigrate(cmd *cobra.Command, args []string) error {
	cfg := migrateConfig{}
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("config parse: %w", err)
	}

	// Use absolute path for migrations in container, relative for local development
	migrationsPath := "file://migrations"
	if _, err := os.Stat("/migrations"); err == nil {
		migrationsPath = "file:///migrations"
	}

	// cmd is nil when runMigrate is called directly rather than through cobra,
	// which is how the configuration tests reach it.
	var out io.Writer = os.Stdout
	var in io.Reader = os.Stdin
	if cmd != nil {
		out, in = cmd.OutOrStdout(), cmd.InOrStdin()
	}

	return executeMigrate(migrateEnv{
		DatabaseURL:    cfg.DatabaseURL,
		MigrationsPath: migrationsPath,
		Environment:    cfg.Environment,
		Out:            out,
		In:             in,
	}, args)
}

func executeMigrate(e migrateEnv, args []string) error {
	action := args[0]

	// `create` writes files and never opens a connection; demanding a
	// reachable database to scaffold two empty .sql files is the sort of
	// mismatch between a command's name and its requirements this ticket is
	// about.
	if action == "create" {
		if len(args) < 2 {
			return errors.New("create: give the migration a name, e.g. `teamup migrate create add_widgets`")
		}
		return createMigrationFiles(e, strings.Join(args[1:], "_"))
	}

	m, drv, db, err := openMigrator(e)
	if err != nil {
		return err
	}
	defer db.Close()

	dbName := currentDatabase(db, e.DatabaseURL)

	switch action {
	case "up":
		return migrateUp(e, m, drv, dbName, args[1:])
	case "down":
		return migrateDownOne(e, m, drv, dbName, args[1:])
	case "down-all":
		return migrateDownAll(e, m, drv, db, dbName, args[1:])
	case "version":
		return migrateShowVersion(e, drv, dbName)
	case "force":
		return migrateForce(e, m, drv, dbName, args[1:])
	case "down_all", "downall", "reset", "drop":
		return fmt.Errorf("unknown action %q: the command that rolls everything back is `teamup migrate down-all`", action)
	default:
		return fmt.Errorf("unknown action %q: expected up, down, down-all, version, force or create", action)
	}
}

// openMigrator builds the migrator over a *sql.DB we keep hold of.
//
// migrate.New() would be shorter, but it hides both the database driver and
// the connection. The driver is needed because *migrate.Migrate.Version()
// collapses "no migrations applied" and "dirty" into a discarded error, and
// the connection is needed to tell the operator what down-all is about to
// destroy. Neither is reachable through the URL constructor.
func openMigrator(e migrateEnv) (*migrate.Migrate, database.Driver, *sql.DB, error) {
	db, err := sql.Open("pgx", e.DatabaseURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("migrate: open database: %w", err)
	}
	drv, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		db.Close()
		return nil, nil, nil, fmt.Errorf("migrate: connect database: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance(e.MigrationsPath, "postgres", drv)
	if err != nil {
		db.Close()
		return nil, nil, nil, fmt.Errorf("migrate: read migrations from %s: %w", e.MigrationsPath, err)
	}
	return m, drv, db, nil
}

// --- actions ------------------------------------------------------------

func migrateUp(e migrateEnv, m *migrate.Migrate, drv database.Driver, dbName string, rest []string) error {
	if len(rest) > 0 {
		return fmt.Errorf("up takes no arguments; it applies every pending migration")
	}
	before, err := checkedVersion(drv, dbName)
	if err != nil {
		return err
	}

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			fmt.Fprintf(e.Out, "Already at version %s; no migrations to apply\n", formatVersion(before))
			return nil
		}
		return migrationFailed("up", dbName, drv, err)
	}

	after, _, _ := readVersion(drv)
	fmt.Fprintf(e.Out, "Applied migrations on %q: version %s -> %s\n",
		dbName, formatVersion(before), formatVersion(after))
	return nil
}

// migrateDownOne is what `down` should always have been: one migration, the
// last one, matching both the command's name and the Makefile's "Rollback last
// migration". It used to call m.Down(), which unwinds the entire schema.
func migrateDownOne(e migrateEnv, m *migrate.Migrate, drv database.Driver, dbName string, rest []string) error {
	if len(rest) > 0 {
		return fmt.Errorf(
			"down takes no arguments: it rolls back exactly one migration. Run it again for another step, or `teamup migrate down-all` to roll back everything")
	}
	before, err := checkedVersion(drv, dbName)
	if err != nil {
		return err
	}
	if before == noMigrationsApplied {
		fmt.Fprintf(e.Out, "Nothing to roll back on %q: no migrations are applied\n", dbName)
		return nil
	}

	if err := m.Steps(-1); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			fmt.Fprintf(e.Out, "Nothing to roll back on %q: no migrations are applied\n", dbName)
			return nil
		}
		return migrationFailed("down", dbName, drv, err)
	}

	after, _, _ := readVersion(drv)
	// Saying which version it moved from and to is the whole difference
	// between a rollback and an accident: silence is how somebody runs it
	// twice without noticing the first one worked.
	fmt.Fprintf(e.Out, "Rolled back one migration on %q: version %s -> %s\n",
		dbName, formatVersion(before), formatVersion(after))
	return nil
}

// migrateDownAll is the destructive one, and is named so that nobody reaches
// it by muscle memory or by a script that meant `down`.
//
// It has no --yes flag on purpose. The confirmation is the entire safety
// property, and a flag that switches it off is the first thing any script
// would add — at which point the accident this exists to prevent is back, now
// with a rubber stamp. Automation that wants an empty database should drop and
// recreate the database instead, which is what the test harness does.
func migrateDownAll(e migrateEnv, m *migrate.Migrate, drv database.Driver, db *sql.DB, dbName string, rest []string) error {
	if len(rest) > 0 {
		return fmt.Errorf("down-all takes no arguments")
	}

	before, err := checkedVersion(drv, dbName)
	if err != nil {
		return err
	}

	if isProductionEnvironment(e.Environment) {
		return fmt.Errorf(
			"refusing to roll back every migration: TEAMUP_ENV=%s.\n"+
				"down-all drops every table in %q and its only undo is a restore from backup, "+
				"so it has no override in production. If a production schema really must be "+
				"torn down, do it deliberately from a maintenance host with TEAMUP_ENV set to "+
				"something else, and take the backup first",
			e.Environment, dbName)
	}

	if before == noMigrationsApplied {
		fmt.Fprintf(e.Out, "Nothing to roll back on %q: no migrations are applied\n", dbName)
		return nil
	}

	// Say what is about to be lost, in the units the operator cares about,
	// before asking. Counts are exact rather than the planner's estimates:
	// a number that might be wrong is worse than no number at all when it is
	// the last thing shown before something irreversible.
	ctx := context.Background()
	tables, rows, err := schemaFootprint(ctx, db)
	if err != nil {
		return fmt.Errorf("down-all: measure what would be lost: %w", err)
	}

	fmt.Fprintf(e.Out, "\nAbout to roll back ALL %s migrations.\n\n", formatVersion(before))
	fmt.Fprintf(e.Out, "  database:  %s\n", dbName)
	if host := displayHost(e.DatabaseURL); host != "" {
		fmt.Fprintf(e.Out, "  server:    %s\n", host)
	}
	fmt.Fprintf(e.Out, "  version:   %s -> none\n", formatVersion(before))
	fmt.Fprintf(e.Out, "  destroys:  %d tables, %d rows\n", tables, rows)
	fmt.Fprintf(e.Out, "\nThis cannot be undone except from a backup.\n")

	if err := confirmDatabaseName(e, dbName); err != nil {
		return err
	}

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return migrationFailed("down-all", dbName, drv, err)
	}

	after, _, _ := readVersion(drv)
	fmt.Fprintf(e.Out, "Rolled back every migration on %q: version %s -> %s\n",
		dbName, formatVersion(before), formatVersion(after))
	return nil
}

func migrateShowVersion(e migrateEnv, drv database.Driver, dbName string) error {
	version, dirty, err := readVersion(drv)
	if err != nil {
		return fmt.Errorf("migrate version: %w", err)
	}

	state := "clean"
	if dirty {
		state = "dirty"
	}
	if version == noMigrationsApplied {
		fmt.Fprintf(e.Out, "Database %q: no migrations applied (%s)\n", dbName, state)
	} else {
		fmt.Fprintf(e.Out, "Database %q: version %d (%s)\n", dbName, version, state)
	}

	// `version` is one of the two commands that still works while dirty, so
	// it is where the operator most likely first sees the problem.
	if dirty {
		fmt.Fprintln(e.Out)
		fmt.Fprintln(e.Out, dirtyGuidance(dbName, version))
	}
	return nil
}

// migrateForce records which version is true, clearing the dirty mark. It is
// the escape hatch out of a half-applied migration, and it was unreachable:
// the version was parsed with an unchecked `fmt.Sscanf` into a uint, so
// `force -1` and `force banana` both left it at zero and reported success —
// recording version 0, which has no migration file and wedges `up` outright.
func migrateForce(e migrateEnv, m *migrate.Migrate, drv database.Driver, dbName string, rest []string) error {
	if len(rest) < 1 {
		return errors.New("force: give the version to record, or `nil` for \"no migrations applied\"")
	}
	if len(rest) > 1 {
		return fmt.Errorf("force takes one version, got %d arguments", len(rest))
	}

	version, err := parseForceVersion(rest[0])
	if err != nil {
		return err
	}
	if version != noMigrationsApplied {
		if err := requireMigrationExists(e.MigrationsPath, version); err != nil {
			return err
		}
	}

	before, dirty, _ := readVersion(drv)
	if err := m.Force(int(version)); err != nil {
		return fmt.Errorf("migrate force: %w", err)
	}

	fmt.Fprintf(e.Out, "Recorded %q at version %s (was %s%s). The schema itself was not touched.\n",
		dbName, formatVersion(version), formatVersion(before), map[bool]string{true: ", dirty"}[dirty])
	return nil
}

// --- version plumbing ---------------------------------------------------

// readVersion asks the database driver rather than *migrate.Migrate, because
// Migrate.Version() reports "no migrations applied" as an error and throws the
// dirty flag away with it — which is exactly the state a failed rollback
// leaves behind.
func readVersion(drv database.Driver) (int64, bool, error) {
	v, dirty, err := drv.Version()
	if err != nil {
		return 0, false, err
	}
	return int64(v), dirty, nil
}

// checkedVersion reads the version and refuses to go further if the database
// is dirty, so every command that would touch the schema fails with the same
// explanation instead of the library's "Dirty database version N. Fix and
// force version."
func checkedVersion(drv database.Driver, dbName string) (int64, error) {
	version, dirty, err := readVersion(drv)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if dirty {
		return 0, errors.New(dirtyGuidance(dbName, version))
	}
	return version, nil
}

// migrationFailed wraps a mid-migration failure with what it did to the
// database. golang-migrate marks the version dirty before running a migration
// and clears it afterwards, so a failure leaves the mark behind and every
// later command refuses — which is how the reported database ended up being
// dropped and recreated.
func migrationFailed(action, dbName string, drv database.Driver, cause error) error {
	version, dirty, err := readVersion(drv)
	if err != nil || !dirty {
		return fmt.Errorf("migrate %s: %w", action, cause)
	}
	return fmt.Errorf("migrate %s failed partway:\n\n%w\n\n%s", action, cause, dirtyGuidance(dbName, version))
}

// dirtyGuidance is the message an operator meets after a migration died
// halfway. The library's own wording ("Fix and force version.") says neither
// what to fix nor what to force it to.
func dirtyGuidance(dbName string, version int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "database %q is marked dirty at version %s.\n\n", dbName, formatVersion(version))
	b.WriteString(
		"golang-migrate marks the version it is moving to and clears the mark once the\n" +
			"migration finishes, so a mark left behind means one failed partway. The schema\n" +
			"is therefore not guaranteed to match that version, nor the one either side of\n" +
			"it, and every command that would migrate refuses until somebody says which\n" +
			"version is actually true. Nothing here can work that out for you.\n\n")
	b.WriteString("  1. Read the migrations around that version and compare them with the\n" +
		"     schema to see how far it got. `teamup migrate version` is safe to run.\n")
	b.WriteString("  2. Finish or undo the remainder by hand.\n")
	b.WriteString("  3. Record what is now true, which clears the mark:\n")
	if version == noMigrationsApplied {
		fmt.Fprintf(&b, "       teamup migrate force nil   # the schema is empty\n")
		fmt.Fprintf(&b, "       teamup migrate force 1     # migration 1 is fully applied\n")
	} else {
		fmt.Fprintf(&b, "       teamup migrate force %d\n", version)
		if version <= 1 {
			fmt.Fprintf(&b, "       teamup migrate force nil   # if the schema is now empty\n")
		} else {
			fmt.Fprintf(&b, "       teamup migrate force %d\n", version-1)
		}
	}
	return b.String()
}

func formatVersion(v int64) string {
	if v <= noMigrationsApplied {
		return "none"
	}
	return strconv.FormatInt(v, 10)
}

func parseForceVersion(raw string) (int64, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "nil", "none":
		return noMigrationsApplied, nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf(
			"force: %q is not a version. Give a migration number, or `nil` to record that no migration is applied",
			raw)
	}
	if v < noMigrationsApplied {
		return 0, fmt.Errorf("force: version %d does not exist; the lowest is `nil` (no migrations applied)", v)
	}
	if v == 0 {
		// Version 0 has no migration file, and recording it leaves `up`
		// unable to do anything at all ("no migration found for version 0").
		return 0, errors.New("force: there is no version 0; use `nil` to record that no migration is applied")
	}
	return v, nil
}

// requireMigrationExists stops `force` from recording a version that has no
// migration file, which bricks `up` rather than unblocking it.
func requireMigrationExists(source string, version int64) error {
	dir, ok := migrationsDir(source)
	if !ok {
		return nil // not a local directory; nothing to check against
	}
	versions, err := migrationVersions(dir)
	if err != nil {
		return err
	}
	for _, v := range versions {
		if v == version {
			return nil
		}
	}
	return fmt.Errorf("force: no migration numbered %d exists in %s", version, dir)
}

// --- down-all support ---------------------------------------------------

func isProductionEnvironment(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "production", "prod":
		return true
	}
	return false
}

// confirmDatabaseName makes the operator type the thing they are about to
// destroy. "y" is too easy to type by reflex; the database name is not.
func confirmDatabaseName(e migrateEnv, dbName string) error {
	fmt.Fprintf(e.Out, "\nType the database name (%s) to continue: ", dbName)

	line, err := bufio.NewReader(e.In).ReadString('\n')
	typed := strings.TrimSpace(line)
	if typed == "" {
		if err != nil {
			return fmt.Errorf(
				"down-all not confirmed: nothing to read on stdin, so this is not an interactive terminal. "+
					"Nothing was changed. down-all has no unattended mode by design; to reset a database from a "+
					"script, drop and recreate %q instead", dbName)
		}
		return fmt.Errorf("down-all not confirmed: nothing typed. Nothing was changed")
	}
	if typed != dbName {
		return fmt.Errorf("down-all not confirmed: %q is not the name of this database (%q). Nothing was changed", typed, dbName)
	}
	return nil
}

// schemaFootprint counts what down-all would destroy.
func schemaFootprint(ctx context.Context, db *sql.DB) (tables int, rows int64, err error) {
	names, err := applicationTableNames(ctx, db)
	if err != nil {
		return 0, 0, err
	}
	if len(names) == 0 {
		return 0, 0, nil
	}
	counts := make([]string, 0, len(names))
	for _, n := range names {
		counts = append(counts, fmt.Sprintf("(SELECT count(*) FROM %s)", quoteIdentifier(n)))
	}
	if err := db.QueryRowContext(ctx, "SELECT "+strings.Join(counts, " + ")).Scan(&rows); err != nil {
		return 0, 0, err
	}
	return len(names), rows, nil
}

// applicationTableNames lists the tables the migrations own — every table in
// the public schema except golang-migrate's own bookkeeping.
func applicationTableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT tablename FROM pg_tables
		  WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
		  ORDER BY tablename`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// currentDatabase asks the server what it is called, falling back to the DSN
// if the query fails. The name is what the operator has to type to confirm a
// down-all, so it must be the server's answer and not our parsing of a URL.
func currentDatabase(db *sql.DB, dsn string) string {
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err == nil && name != "" {
		return name
	}
	if u, err := url.Parse(dsn); err == nil {
		return strings.TrimPrefix(u.Path, "/")
	}
	return "unknown"
}

// displayHost renders where the database lives, without the credentials.
func displayHost(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return u.Host
}

// --- create -------------------------------------------------------------

var migrationFileName = regexp.MustCompile(`^(\d+)_.*\.(up|down)\.sql$`)

// migrationName is the shape every file in migrations/ already has.
var migrationName = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// createMigrationFiles scaffolds the next migration.
//
// It used to stamp a `20060102150405` timestamp onto a directory numbered
// 0001..0014 and write into whatever `./migrations` resolved to from the
// caller's working directory — so it produced a file that did not match its
// neighbours, in a place that depended on where you stood.
func createMigrationFiles(e migrateEnv, rawName string) error {
	dir, ok := migrationsDir(e.MigrationsPath)
	if !ok {
		return fmt.Errorf("create: migrations source %q is not a local directory", e.MigrationsPath)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("create: no migrations directory at %s (run this from the repository root)", dir)
	}

	name, err := normalizeMigrationName(rawName)
	if err != nil {
		return err
	}

	versions, err := migrationVersions(dir)
	if err != nil {
		return err
	}
	next := int64(1)
	if len(versions) > 0 {
		next = versions[len(versions)-1] + 1
	}

	stem := fmt.Sprintf("%04d_%s", next, name)
	created := time.Now().Format(time.RFC3339)
	files := []struct{ path, content string }{
		{filepath.Join(dir, stem+".up.sql"), fmt.Sprintf("-- Migration: %s\n-- Created: %s\n\n", name, created)},
		{filepath.Join(dir, stem+".down.sql"), fmt.Sprintf("-- Rollback: %s\n-- Created: %s\n\n", name, created)},
	}
	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil {
			return fmt.Errorf("create: %s already exists", f.path)
		}
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			return fmt.Errorf("create: write %s: %w", f.path, err)
		}
	}

	fmt.Fprintf(e.Out, "Created migration files:\n  %s\n  %s\n", files[0].path, files[1].path)
	fmt.Fprintf(e.Out, "\nThe rollback is not optional: `teamup migrate down` will run %s.down.sql,\nand a rollback that cannot run is how a database ends up stuck.\n", stem)
	return nil
}

func normalizeMigrationName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.NewReplacer(" ", "_", "-", "_").Replace(name)
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	name = strings.Trim(name, "_")
	if !migrationName.MatchString(name) {
		return "", fmt.Errorf(
			"create: %q cannot be a migration name; use lower-case words separated by underscores, e.g. add_widgets", raw)
	}
	return name, nil
}

// migrationVersions returns the versions present in the directory, ascending.
func migrationVersions(dir string) ([]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory %s: %w", dir, err)
	}
	seen := map[int64]bool{}
	for _, entry := range entries {
		match := migrationFileName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		v, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %q has an unreadable version prefix: %w", entry.Name(), err)
		}
		seen[v] = true
	}
	versions := make([]int64, 0, len(seen))
	for v := range seen {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}

// migrationsDir turns a golang-migrate source URL back into a path, for the
// two things that need the files themselves rather than the source driver.
func migrationsDir(source string) (string, bool) {
	if !strings.HasPrefix(source, "file://") {
		return "", false
	}
	return strings.TrimPrefix(source, "file://"), true
}
