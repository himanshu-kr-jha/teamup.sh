# 35 — Persist the JWT signing key instead of generating one per process

**What to build:** The RSA keypair that signs access tokens is generated fresh in `auth.NewService` on every process start and never persisted. Two replicas therefore reject each other's tokens, and every restart invalidates every access token in the field. `TEAMUP_AUTH_SECRET` is parsed and documented but wired to nothing. Load the key from configuration (or persist a generated one), and make the key material a real, validated setting.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Failing test: a token minted by one `auth.Service` validates in a second `auth.Service` built from the same configuration
- [x] Source the key from config — a PEM private key, or derive/persist deterministically
- [x] Either wire `TEAMUP_AUTH_SECRET` to something real or delete it from `Config`, `.env.example` and `docker-compose.prod.yml`
- [ ] Fail loudly at boot when no key is configured and the deployment has `replicaCount > 1` — superseded, see comments
- [x] Stop discarding the `rsa.GenerateKey` error

**Type:** bug

## Evidence

`internal/auth/auth.go:126-132`:

```go
func NewService(db *pgxpool.Pool, redis *redis.Client, cfg Config) *Service {
	// Generate RSA keys if not provided
	if cfg.PrivateKey == nil {
		privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		cfg.PrivateKey = privateKey
		cfg.PublicKey = &privateKey.PublicKey
	}
```

`cfg.PrivateKey` is *never* populated by the application. `Config.AuthConfig()` at `cmd/teamup/main.go:205-222` builds the `auth.Config` and sets no `PrivateKey`/`PublicKey`. Signing uses it at `auth.go:190`, verification at `auth.go:281`.

`TEAMUP_AUTH_SECRET` is declared at `cmd/teamup/main.go:138`:
```go
AuthSecret   string `env:"TEAMUP_AUTH_SECRET"` // For HS256 fallback
```
`grep -rn "AuthSecret"` over the whole tree returns only that declaration, `.env.example:31`, and `docker-compose.prod.yml:127`. **Nothing reads it.** An operator who sets it believes they have pinned the signing key and has not.

Also note `rsa.GenerateKey`'s error is discarded with `_`. If it ever failed, `cfg.PrivateKey` would be nil and every login would error at `auth.go:190`.

### Reproduction A — two replicas reject each other (verified)

Two processes from the same binary, same `TEAMUP_DATABASE_URL` (one Postgres), replica B additionally given `TEAMUP_AUTH_SECRET="a-shared-secret-that-operators-think-pins-the-key"`:

- replica A on :8099, replica B on :8098
- Log in against A, take the `at` cookie.

```
GET /api/org  Authorization: Bearer <token from A>   ->  A: 200
GET /api/org  Authorization: Bearer <token from A>   ->  B: 401 {"error":"unauthorized"}
```

Same deployment, same database, same declared secret. Under `helm/teamup/values.yaml` with `replicaCount` raised above 1 (the chart also ships an `autoscaling` block at `values.yaml:66`), a load balancer spreading requests across pods means roughly `(n-1)/n` of all API calls 401. The app is not horizontally scalable as shipped, which contradicts the scaling target recorded for the LiveKit decision in the map.

### Reproduction B — refresh stampede on restart, and a rotation race

Because tokens die with the process, every client refreshes at once after any restart or rolling deploy. That path is not concurrency-safe.

With one valid `rt` cookie and a deliberately invalid `at` (which is exactly the state every client is in after a restart), ten parallel requests:

```
for i in $(seq 1 10); do
  curl -s -o /dev/null -w "%{http_code}\n" \
    -H "Cookie: at=garbage.token.value; rt=$RT" http://localhost:8099/api/org &
done
```

Result: **one 200 and nine 401s.**

`AuthMiddleware` (`auth.go:386-397`) falls back to `RefreshAccessToken`, which revokes the presented refresh token (`auth.go:242-246`) before issuing a new one. The first caller rotates it; every concurrent caller then presents a token whose `RevokedAt` is set and is rejected at `auth.go:237`. A PWA refetching several queries at once (TanStack Query on window focus, per the frontend decision) will bounce the user to the sign-in screen.

Fixing the key persistence removes the restart stampede; the rotation race deserves its own guard (grace window for a just-rotated token, or single-flight per session).

### Also worth correcting while here

`cmd/teamup/main.go:219` sets `RefreshTokenTTL: 7 * 24 * time.Hour`. The OIDC decision in the map specifies 30-day refresh tokens.

## Comments

**Root cause.** Two separate defects, both in `internal/auth`:

1. `NewService` minted an RSA keypair per process and there was no code path anywhere that could give it one instead. The key was therefore per-process state for something that is per-deployment state, so a token was only ever verifiable by the exact process that signed it.
2. `RefreshAccessToken` revoked the presented refresh token in one statement and inserted the replacement in another, with no lock between them. Callers arriving together all passed the "is it revoked?" check or all failed it depending on who committed first, and the losers were answered 401.

**Design choice: store the key in the database, sealed with `TEAMUP_AUTH_SECRET`.**

Deriving the key deterministically from the secret was the first candidate and does not work for RSA: `crypto/rsa` deliberately consumes a random amount of entropy (`randutil.MaybeReadByte`), so the same seed does not reproduce the same key even twice in one process. Making derivation work would have meant moving access tokens to Ed25519 — a change to the token algorithm and its verification, which is a much larger blast radius than this defect justifies, and the ticket allows persisting instead.

So `signing_keys` (migration `0010`) holds one row: `kid`, `algorithm`, `wrapping`, `private_key`, `created_at`. `loadOrCreateSigningKey` takes `pg_advisory_xact_lock` and re-reads inside the transaction before inserting — the same shape as `wizard.FinalizeSetup` — so N replicas booting at once against an empty database settle on one key rather than racing to store several. The Helm chart never sets `TEAMUP_AUTH_SECRET` at all, so a key that needs no operator action was the only option that makes `replicaCount > 1` work out of the box.

`TEAMUP_AUTH_SECRET` is now the wrapping key for that row: HKDF-SHA256 to 32 bytes, AES-256-GCM, nonce first. It is optional, but once used it must keep being used — booting with the wrong value or with it removed fails with a message saying so, rather than quietly minting a new key and signing everybody out. Setting it on a deployment that started without it encrypts the key already in use rather than demanding a new one. This is what makes the setting real; deleting it instead would have collided with ticket 46, which plans to require it in production.

The remaining checklist item — fail loudly when no key is configured and `replicaCount > 1` — is superseded rather than skipped: there is no longer a configuration in which replicas disagree, so there is nothing to warn about. An explicit `TEAMUP_AUTH_PRIVATE_KEY` PEM override was deliberately not added; `Config.PrivateKey` is still honoured if a caller supplies one, so the seam exists if an operator ever needs to bring their own key.

**Rotation race.** `RefreshAccessToken` now runs in one transaction and takes `FOR UPDATE` on the presented token, which serialises concurrent callers. The winner writes the replacement and records `refresh_tokens.replaced_by` (migration `0011`). A caller that finds the token already rotated, within `refreshRotationGrace` (30s), is handed an access token **on the replacement's session** and **no refresh token** — so a burst from one client all succeeds, exactly one rotation happens, and nothing hands out a second long-lived credential. Outside the window, or if the token was revoked by signing out rather than rotated, it is refused as before. `SetAuthCookies` no longer overwrites the `rt` cookie when the pair carries no refresh token.

This is a deliberate behaviour change: `TestRefreshAccessTokenRotatesAndInvalidatesTheOldToken` used to assert that a replayed token fails immediately, and now asserts that it never yields another refresh token and that its session is finished with. `TestARotatedTokenIsDeadOnceTheGraceWindowPasses` covers the point where it stops working altogether.

**Tests added** — `internal/auth/signing_key_test.go`:

- `TestATokenFromOneReplicaValidatesOnAnother:75` — the headline reproduction
- `TestTheSigningKeySurvivesARestart:99`
- `TestConcurrentFirstBootAgreesOnOneKey:121` — eight replicas booting at once against an empty table
- `TestATokenSignedWithAnotherKeyIsRejected:166` — a foreign-key-signed token and an `alg=none` token both stay rejected
- `TestConcurrentRefreshDoesNotSignTheUserOut:227`
- `TestConcurrentRequestsThroughTheRefreshFallbackAllSucceed:276` — the ticket's stampede, through the middleware
- `TestARotatedTokenIsDeadOnceTheGraceWindowPasses:317`
- `TestTheAuthSecretEncryptsTheStoredKey:371`
- `TestSettingTheAuthSecretLaterEncryptsTheKeyInUse:408`

RED before the fix: cross-replica and restart failed with `token signature is invalid: crypto/rsa: verification error`; concurrent boot with "replica 1 booted with a different signing key than replica 0"; the stampede with one success and nine `UNAUTHORIZED`.

**Verified end to end** with two processes of the built binary on :8099 and :8098 against one database: token from A → A 200, B 200; A restarted → 200; one row in `signing_keys`, `wrapping = aes-256-gcm`; the ten-parallel-request stampede → ten 200s.

**Also done:** `RefreshTokenTTL` raised from 7 to 30 days to match the OIDC decision. `README.md` and `docker-compose.prod.yml` now say what `TEAMUP_AUTH_SECRET` does.

**Left alone:** key rotation (the schema carries `kid` and takes the oldest row, so a second key can be added later, but nothing rotates today and access tokens carry no `kid` header); the `.env.example` the README refers to, which does not exist in the repository.
