package main

import (
	"os"
	"strings"
	"testing"

	"github.com/caarlos0/env/v10"
)

// Every TEAMUP_* variable a fully configured server needs. Tests build their
// environment from this and take away only what they mean to be missing, so a
// new required setting shows up in one place.
func completeServerEnv() map[string]string {
	return map[string]string{
		"TEAMUP_DATABASE_URL":       "postgres://teamup:teamup@localhost:5432/teamup?sslmode=disable",
		"TEAMUP_REDIS_URL":          "redis://localhost:6379/0",
		"TEAMUP_MINIO_ENDPOINT":     "localhost:9000",
		"TEAMUP_MINIO_ACCESS_KEY":   "minioadmin",
		"TEAMUP_MINIO_SECRET_KEY":   "minioadmin",
		"TEAMUP_MINIO_BUCKET":       "teamup-files",
		"TEAMUP_LIVEKIT_URL":        "ws://localhost:7880",
		"TEAMUP_LIVEKIT_API_KEY":    "devkey",
		"TEAMUP_LIVEKIT_API_SECRET": "devsecret",
	}
}

// The service variables migrations have no business needing.
var serviceEnvKeys = []string{
	"TEAMUP_REDIS_URL",
	"TEAMUP_MINIO_ENDPOINT",
	"TEAMUP_MINIO_ACCESS_KEY",
	"TEAMUP_MINIO_SECRET_KEY",
	"TEAMUP_MINIO_BUCKET",
	"TEAMUP_LIVEKIT_URL",
	"TEAMUP_LIVEKIT_API_KEY",
	"TEAMUP_LIVEKIT_API_SECRET",
}

// onlyEnv makes the process environment contain exactly the given TEAMUP_*
// variables for the duration of the test: everything else starting TEAMUP_ is
// removed and restored afterwards. Without this a test would pass or fail
// depending on what the developer's shell exports, and "migrate does not need
// MinIO" would go green on a machine that happens to have MinIO configured.
func onlyEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "TEAMUP_") {
			continue
		}
		if _, wanted := vars[key]; wanted {
			continue
		}
		unsetEnv(t, key)
	}
	for key, value := range vars {
		t.Setenv(key, value)
	}
}

// unsetEnv removes a variable and puts it back when the test finishes. The
// standard library has t.Setenv but no t.Unsetenv, and setting a variable to
// the empty string is not the same thing: env's `required` check asks whether
// the variable exists, not whether it has a value.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			os.Setenv(key, previous)
		}
	})
}

// Migrations touch Postgres only. Demanding Redis, object storage and LiveKit
// credentials to run them stops `make test-db`, and stops a pre-deploy job or
// init container — which has the database but none of the service credentials
// — from migrating at all.
func TestMigrateDoesNotRequireServiceConfig(t *testing.T) {
	onlyEnv(t, map[string]string{
		"TEAMUP_DATABASE_URL": completeServerEnv()["TEAMUP_DATABASE_URL"],
	})

	// `version` is the cheapest action. It gets no further than migrate.New,
	// which fails here because the migrations directory is not below this
	// package — a database-layer failure, which is exactly the point: config
	// parsing must already be behind us.
	err := runMigrate(nil, []string{"version"})
	if err == nil {
		return // Nothing to complain about; config certainly parsed.
	}
	if strings.Contains(err.Error(), "config parse") {
		t.Fatalf("migrate refused to load its configuration: %v", err)
	}
	for _, key := range serviceEnvKeys {
		if strings.Contains(err.Error(), key) {
			t.Errorf("migrate must not require %s, got: %v", key, err)
		}
	}
}

// The database URL is the one thing migrations cannot do without, so it stays
// required — scoping the configuration must not turn into not checking it.
func TestMigrateStillRequiresTheDatabaseURL(t *testing.T) {
	onlyEnv(t, nil)

	err := runMigrate(nil, []string{"version"})
	if err == nil {
		t.Fatal("migrate with no database URL must fail")
	}
	if !strings.Contains(err.Error(), "TEAMUP_DATABASE_URL") {
		t.Fatalf("error should name the missing database URL, got: %v", err)
	}
}

// The server is a different matter: it needs all of it, and says so at boot
// rather than at the first request that reaches for the missing piece. This
// guards against "fix" the migrate path by dropping the required tags.
//
// runServer parses this struct, so what is asserted here is what it enforces.
func TestServerConfigStillRequiresEveryService(t *testing.T) {
	for _, missing := range serviceEnvKeys {
		t.Run(missing, func(t *testing.T) {
			vars := completeServerEnv()
			delete(vars, missing)
			onlyEnv(t, vars)

			cfg := Config{}
			err := env.Parse(&cfg)
			if err == nil {
				t.Fatalf("server config parsed without %s", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("error should name %s, got: %v", missing, err)
			}
		})
	}
}

// ...and a complete environment still parses, with the database URL reaching
// the field the server actually reads.
func TestServerConfigParsesCompleteEnvironment(t *testing.T) {
	vars := completeServerEnv()
	onlyEnv(t, vars)

	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		t.Fatalf("complete environment should parse: %v", err)
	}
	if cfg.DatabaseURL != vars["TEAMUP_DATABASE_URL"] {
		t.Errorf("database URL = %q, want %q", cfg.DatabaseURL, vars["TEAMUP_DATABASE_URL"])
	}
	if cfg.MinIOBucket != vars["TEAMUP_MINIO_BUCKET"] {
		t.Errorf("minio bucket = %q, want %q", cfg.MinIOBucket, vars["TEAMUP_MINIO_BUCKET"])
	}
}
