# 46 — Validate configuration per subcommand and per environment

**What to build:** The config layer is one flat struct validated identically for every code path. It over-constrains commands that need almost none of it, and under-constrains the settings that actually decide whether a production deployment is safe. Scope required settings to the command that uses them, and validate the production-critical ones.

**Blocked by:** 32 (same config layer; coordinate so the two do not conflict)

**Status:** open

- [ ] Failing test: `TEAMUP_ENV=production` with `TEAMUP_INSECURE_COOKIES=true` refuses to boot
- [ ] Failing test: `TEAMUP_ENV=production` with `TEAMUP_BASE_URL` left at its localhost default refuses to boot
- [ ] Split required-field validation per subcommand rather than parsing one global struct
- [ ] Remove or implement `TEAMUP_AUTH_SECRET` (ticket 35)
- [ ] Log loudly at startup when Web Push is disabled for want of VAPID keys
- [ ] Allow unattended setup without an external IdP, or document that an IdP is mandatory

**Type:** task

## Evidence

`Config` at `cmd/teamup/main.go:111-202` marks Redis, all four MinIO settings and all three LiveKit settings `,required`. Both `runServer` (`main.go:315-318`) and `runMigrate` (`main.go:631-633`) call `env.Parse(&cfg)` on that same struct.

### Over-constrained

`migrate` needs only `TEAMUP_DATABASE_URL` — `runMigrate` reads `cfg.DatabaseURL` at `main.go:642` and nothing else — yet refuses to run without eight unrelated variables. That is ticket 32; not re-filed here.

Surveying the rest of the surface for the same shape: `version` and `wizard` (`main.go:287-304`) never call `env.Parse`, so they are unaffected. `migrate` is the only over-constrained command. The blast radius of the global-validation defect is therefore narrower than it looks — one command, plus the operational friction of every `migrate` invocation in a Helm pre-install hook or entrypoint script needing the full production environment.

### Under-constrained — the more consequential half

Settings that decide whether a production deployment is safe have no validation at all:

- **`TEAMUP_BASE_URL`** (`main.go:141`) defaults to `http://localhost:8080` and is never checked. It flows into `AuthConfig()` (`main.go:209`) as the OIDC redirect base. A production instance that forgets it builds redirect URIs pointing at localhost, and OIDC sign-in fails in a way that looks like a provider misconfiguration.
- **`TEAMUP_INSECURE_COOKIES`** (`main.go:151`) drops the `Secure` flag from auth cookies (`auth.go:58`, used at `auth.go:334`, `:345`, `:360`). Its own comment says it is for local-network testing "and nowhere else", but nothing stops `TEAMUP_ENV=production` from running with it on and shipping session cookies over plaintext.
- **`TEAMUP_AUTH_SECRET`** (`main.go:138`) is parsed, documented in `.env.example:31`, and wired in `docker-compose.prod.yml:127` — and read by nothing. See ticket 35.
- **VAPID keys** (`main.go:171-173`) are optional with no warning. Absent both these and stored wizard keys, Web Push is silently off; `loadStoredVAPIDKeys` (`main.go:867-869`) logs only at `Info`.
- **`TEAMUP_ALLOWED_WS_ORIGINS`** (`main.go:146`) is passed unvalidated into `websocket.Accept`'s `OriginPatterns` (`hub.go:444`). A malformed entry silently fails to match rather than being rejected at boot.

### Unattended setup requires an external IdP (verified)

`TEAMUP_AUTO_SETUP=true` with a complete `TEAMUP_SETUP_*` environment but no OIDC provider fails at boot:

```
FTL Auto-setup failed
error="auto-setup environment is incomplete (setup data incomplete:
oidcProvider must be one of google, entra, custom); set the missing
TEAMUP_SETUP_* variables or disable TEAMUP_AUTO_SETUP"
```

`internal/wizard/state.go:74` requires `binding:"required,oneof=google entra custom"` and `state.go:209-212` enforces it in `Validate`. Setting `TEAMUP_SETUP_OIDC_PROVIDER=custom` with a bogus issuer does succeed — `oidc.NewProvider`'s error is swallowed at `auth.go:153-158` (`if err == nil`), so the instance provisions with a non-functional provider and no warning.

Password authentication is fully implemented and works (`POST /api/auth/login` → `LoginWithPassword`, verified end to end), so a self-hosted startup with no IdP has a working sign-in path but cannot complete setup to reach it. Either relax the requirement or make the constraint explicit in the deployment docs.

### Test shape

Table-driven over `Config`: for each production-hostile combination, assert the validator rejects it with a message naming the setting. Today no such validator exists — `env.Parse` checks only presence, never value.
