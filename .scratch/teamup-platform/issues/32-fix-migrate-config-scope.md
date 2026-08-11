# 32 — Fix migrate: scope config validation to the command

**What to build:** Scope configuration validation to what each subcommand actually needs. `teamup migrate` talks to Postgres and nothing else, so it must load only the database URL. The server keeps validating everything at boot.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Split the database URL out of `Config` into a `DatabaseConfig` the migrate command parses on its own
- [x] Keep `Config` — the server's configuration — requiring every service credential, so a misconfigured server still fails at boot
- [x] Cover both with tests that set the environment explicitly rather than inheriting the shell's
- [x] Make `make test-db` work with only `TEAMUP_DATABASE_URL` set
- [x] Make `test-db` recreate the database `TEST_DATABASE_URL` names instead of a hardcoded `teamup_test`

**Type:** bug

## Evidence

`teamup migrate up` refused to run without the full service configuration:

```
$ ./bin/teamup migrate up
Error: config parse: env: required environment variable "TEAMUP_REDIS_URL" is not set;
  required environment variable "TEAMUP_MINIO_ENDPOINT" is not set;
  required environment variable "TEAMUP_MINIO_ACCESS_KEY" is not set;
  required environment variable "TEAMUP_MINIO_SECRET_KEY" is not set;
  required environment variable "TEAMUP_MINIO_BUCKET" is not set;
  required environment variable "TEAMUP_LIVEKIT_URL" is not set;
  required environment variable "TEAMUP_LIVEKIT_API_KEY" is not set;
  required environment variable "TEAMUP_LIVEKIT_API_SECRET" is not set
```

Migrations touch Postgres only, so none of those are relevant. The blast radius:

- `make test-db` (Makefile) runs `bin/teamup migrate up` with only `TEAMUP_DATABASE_URL` set, so it failed for every fresh contributor. Their DB-backed tests then skipped silently and a green suite meant nothing.
- Running migrations from a pre-deploy job or an init container — which has database access but no service credentials — was impossible.

## Comments

**Root cause.** `runMigrate` and `runServer` parsed the same `Config` struct, and every service credential on it is tagged `,required`. `caarlos0/env` validates the whole struct in one pass, so the migrate command inherited the server's boot-time requirements wholesale. Nothing about the migrate path needed those values; it only ever read `cfg.DatabaseURL`.

**Fix.** `DatabaseConfig` (`cmd/teamup/main.go:111`) now holds `TEAMUP_DATABASE_URL`, still required. `Config` embeds it and is otherwise unchanged, so `runServer` validates exactly what it did before and `cfg.DatabaseURL` still resolves through the promoted field. `runMigrate` parses `DatabaseConfig` alone.

**Tests** — `cmd/teamup/config_test.go`:

- `TestMigrateDoesNotRequireServiceConfig` (:83) — the RED. Failed with the error above before the fix.
- `TestMigrateStillRequiresTheDatabaseURL` (:108) — scoping must not become not-checking.
- `TestServerConfigStillRequiresEveryService` (:125) — one subtest per credential; passed before and after, and is the guard against "fix" meaning "delete the required tags".
- `TestServerConfigParsesCompleteEnvironment` (:146).

They install an exact `TEAMUP_*` environment via `onlyEnv` rather than inheriting the shell's, so "migrate does not need MinIO" cannot go green merely because the machine has MinIO configured. Verified by running the RED test against the pre-fix tree with every service variable exported: it still failed.

**Also fixed, and worth a look.** `test-db` substituted a literal `teamup_test` into its DROP/CREATE while migrating whatever `TEST_DATABASE_URL` named. Overriding the variable therefore migrated one database and destroyed a different one. It now derives both the database name and the maintenance connection from the URL. This was in the way of verifying the ticket at all — proving the fix against a scratch database would have dropped the shared `teamup_test`.

**Left alone.** `Config` is still one large struct covering server, auth, OIDC, SMTP, push and auto-setup. Splitting it further is a design change, not this bug. `make test-unit` reports `go: no such tool "covdata"` for five packages; that is a gap in the local Go toolchain (`$GOROOT/pkg/tool/linux_amd64` has no `covdata`), reproduces identically on the unmodified tree, and is not touched here.
