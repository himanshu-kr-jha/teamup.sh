package main

import (
	"os"
	"os/exec"
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

// validServerConfig parses the complete environment, so what the value checks
// below run against is the real Config — defaults included — and not a
// hand-built zero value that never sees envDefault.
func validServerConfig(t *testing.T) Config {
	t.Helper()
	onlyEnv(t, completeServerEnv())

	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		t.Fatalf("complete environment should parse: %v", err)
	}
	return cfg
}

// `env:"...,required"` only asserts that a variable is set. These are the
// values that are set and still unusable: today each one is accepted at boot
// and fails later somewhere that looks unrelated — OIDC redirects built onto a
// malformed base URL, a driver error at dial time naming no setting at all,
// session cookies sent in the clear. The boot has to name the variable at
// fault.
func TestServerConfigRejectsUnusableValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		names  string
	}{
		{"base URL with no scheme or host", func(c *Config) { c.BaseURL = "teamup.example.com" }, "TEAMUP_BASE_URL"},
		{"base URL with no host", func(c *Config) { c.BaseURL = "https://" }, "TEAMUP_BASE_URL"},
		{"base URL that is not http", func(c *Config) { c.BaseURL = "ftp://teamup.example.com" }, "TEAMUP_BASE_URL"},
		{"unparseable database URL", func(c *Config) { c.DatabaseURL = "definitely not a database url" }, "TEAMUP_DATABASE_URL"},
		{"database URL with a bad port", func(c *Config) {
			c.DatabaseURL = "postgres://teamup:teamup@localhost:99999/teamup"
		}, "TEAMUP_DATABASE_URL"},
		{"redis URL with no scheme", func(c *Config) { c.RedisURL = "localhost:6379" }, "TEAMUP_REDIS_URL"},
		{"redis URL with the wrong scheme", func(c *Config) { c.RedisURL = "http://localhost:6379" }, "TEAMUP_REDIS_URL"},
		{"minio endpoint carrying a scheme", func(c *Config) { c.MinIOEndpoint = "http://minio:9000" }, "TEAMUP_MINIO_ENDPOINT"},
		{"minio endpoint with a non-numeric port", func(c *Config) { c.MinIOEndpoint = "minio:ninethousand" }, "TEAMUP_MINIO_ENDPOINT"},
		{"minio endpoint with a port out of range", func(c *Config) { c.MinIOEndpoint = "minio:99999" }, "TEAMUP_MINIO_ENDPOINT"},
		{"livekit URL with no host", func(c *Config) { c.LiveKitURL = "ws://" }, "TEAMUP_LIVEKIT_URL"},
		{"livekit URL with no scheme", func(c *Config) { c.LiveKitURL = "livekit:7880" }, "TEAMUP_LIVEKIT_URL"},
		{"port that is not a number", func(c *Config) { c.Port = "http" }, "TEAMUP_PORT"},
		{"port out of range", func(c *Config) { c.Port = "70000" }, "TEAMUP_PORT"},
		{"smtp port out of range", func(c *Config) { c.SMTPHost = "smtp.example.com"; c.SMTPPort = 70000 }, "TEAMUP_SMTP_PORT"},
		{"auth secret too short to be a key", func(c *Config) { c.AuthSecret = "changeme" }, "TEAMUP_AUTH_SECRET"},
		{"insecure cookies in production", func(c *Config) {
			c.Environment = "production"
			c.InsecureCookies = true
		}, "TEAMUP_INSECURE_COOKIES"},
		{"the example call server secret in production", func(c *Config) {
			c.Environment = "production"
			c.LiveKitAPISecret = "devsecret"
		}, "TEAMUP_LIVEKIT_API_SECRET"},
		{"a short call server secret in production", func(c *Config) {
			c.Environment = "production"
			c.LiveKitAPISecret = "short-but-not-the-example"
		}, "TEAMUP_LIVEKIT_API_SECRET"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validServerConfig(t)
			tc.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("error should name %s, got: %v", tc.names, err)
			}
		})
	}
}

// Shipping session cookies over plain HTTP is only refused where it is
// dangerous; the setting exists so a developer can reach the app by address
// from another machine on their network.
func TestInsecureCookiesAreAllowedOutsideProduction(t *testing.T) {
	cfg := validServerConfig(t)
	cfg.Environment = "development"
	cfg.InsecureCookies = true

	if err := cfg.Validate(); err != nil {
		t.Fatalf("insecure cookies in development should be allowed, got: %v", err)
	}
}

// Settings that are legitimately optional or written in a shorter form must
// not be turned into boot failures.
func TestServerConfigAcceptsUsableValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"the complete environment as it stands", func(*Config) {}},
		{"no auth secret at all", func(c *Config) { c.AuthSecret = "" }},
		{"a generated auth secret", func(c *Config) { c.AuthSecret = strings.Repeat("a1b2c3d4", 8) }},
		{"a minio endpoint with no port", func(c *Config) { c.MinIOEndpoint = "storage.example.com" }},
		{"a secure livekit URL", func(c *Config) { c.LiveKitURL = "wss://livekit.example.com" }},
		{"a livekit URL written as http", func(c *Config) { c.LiveKitURL = "http://livekit:7880" }},
		{"a base URL behind TLS", func(c *Config) { c.BaseURL = "https://teamup.example.com" }},
		{"a keyword/value database DSN", func(c *Config) {
			c.DatabaseURL = "host=localhost port=5432 user=teamup password=teamup dbname=teamup"
		}},
		{"production done properly", func(c *Config) {
			c.Environment = "production"
			c.BaseURL = "https://teamup.example.com"
			c.AuthSecret = strings.Repeat("a1b2c3d4", 8)
			c.LiveKitAPISecret = strings.Repeat("f00dcafe", 8)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validServerConfig(t)
			tc.mutate(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Fatalf("%s should be accepted, got: %v", tc.name, err)
			}
		})
	}
}

// Validation is worth nothing if runServer never asks for it. This drives the
// real binary, with an environment whose every variable is present and one of
// which is dangerous, and expects it to die naming that one.
//
// The database URL points nowhere on purpose: the server must refuse before it
// dials anything, so the message it prints is about the setting and not about
// a connection that was never going to work either.
func TestServerRefusesToBootOnAnUnusableSetting(t *testing.T) {
	bin := serverBinary(t)

	env := map[string]string{
		"TEAMUP_ENV":                "production",
		"TEAMUP_INSECURE_COOKIES":   "true",
		"TEAMUP_DATABASE_URL":       "postgres://teamup:teamup@127.0.0.1:1/teamup?sslmode=disable",
		"TEAMUP_REDIS_URL":          "redis://127.0.0.1:1",
		"TEAMUP_MINIO_ENDPOINT":     "127.0.0.1:9000",
		"TEAMUP_MINIO_ACCESS_KEY":   "minioadmin",
		"TEAMUP_MINIO_SECRET_KEY":   "minioadmin",
		"TEAMUP_MINIO_BUCKET":       "teamup",
		"TEAMUP_LIVEKIT_URL":        "ws://127.0.0.1:7880",
		"TEAMUP_LIVEKIT_API_KEY":    "devkey",
		"TEAMUP_LIVEKIT_API_SECRET": "devsecret",
	}

	cmd := exec.Command(bin, "server")
	cmd.Env = append(os.Environ(), flattenEnv(env)...)
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("the server started with insecure cookies in production:\n%s", out)
	}
	if !strings.Contains(string(out), "TEAMUP_INSECURE_COOKIES") {
		t.Fatalf("the server should have refused by name; it said:\n%s", out)
	}
}
