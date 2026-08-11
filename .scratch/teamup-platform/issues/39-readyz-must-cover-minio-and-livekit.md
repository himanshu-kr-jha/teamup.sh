# 39 — Make /readyz reflect MinIO and LiveKit, not just Postgres and Redis

**What to build:** `/readyz` checks only the database and Redis, so a server that cannot reach MinIO or LiveKit reports itself ready. Kubernetes routes traffic to it and users get 500s on file upload and call creation. Include the storage and media dependencies in the readiness answer.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Failing test: readiness with an unreachable MinIO endpoint returns 503 and names the failing dependency
- [x] Extend `ReadyHandler` to probe MinIO and LiveKit
- [x] Report per-dependency status in the body so the failing one is identifiable
- [x] Keep `/healthz` (liveness) unchanged — a missing MinIO must not restart the pod

**Type:** bug

## Evidence

`cmd/teamup/main.go:452` constructs the handler with two dependencies only:

```go
readyHandler := observability.ReadyHandler(dbPool, redisClient)
```

`internal/observability/observability.go:246-270` checks exactly those two — a `Ping` on the database, then a `Ping` on Redis — and returns `{"status":"ready"}` otherwise. MinIO and LiveKit are never consulted, and `filesService`/`callsService` are not even passed in.

### Reproduction (verified)

**MinIO unreachable** — server started with `TEAMUP_MINIO_ENDPOINT=localhost:9099` (nothing listening), everything else live:

```
healthz=200  readyz=200          <- claims ready
login=200
POST /api/files/upload-url  ->  500 {"error":"failed to generate upload URL"}
```

**LiveKit unreachable** — `TEAMUP_LIVEKIT_URL=ws://localhost:7899`:

```
healthz=200  readyz=200          <- claims ready
login=200
POST /api/calls   ->  500 {"error":"failed to create call"}
GET  /livekit/rtc ->  502 "the call server is unavailable"
```

In both cases the server boots and passes readiness while a headline feature is completely broken.

Contrast with Redis, which is handled correctly: `connectRedis` (`main.go:346-349`, `:830-842`) pings at startup and `runServer` returns an error, so the process exits non-zero with `redis connect: dial tcp 127.0.0.1:6399: connect: connection refused`. That is the right shape — MinIO and LiveKit get neither the boot check nor the readiness check.

The `/livekit` reverse proxy is the one part that degrades well: `proxy.ErrorHandler` at `main.go:543-546` returns a 502 with a readable message.

### Note on which signal to use

Prefer readiness over a hard boot failure for these two. MinIO and LiveKit are separate containers that may start after the app, and hard-failing at boot would produce a crash loop during ordinary startup ordering. Readiness gating gives the right behaviour: the pod comes up, stays out of the load balancer until storage and media are reachable, and joins when they are.

### Test shape

Call `ReadyHandler` with a stub MinIO/LiveKit dependency whose probe returns an error and assert 503 plus a body identifying the failure. Today the handler does not accept those dependencies at all, so the test cannot even be written without the signature change — which is the point.

## Comments

### Root cause

`ReadyHandler(db, redis interface{})` could only ever check two things — the signature had nowhere to put the others, and `filesService`/`callsService` were never passed in. So `/readyz` answered `{"status":"ready"}` for an instance whose uploads and call creation both returned 500.

### Measured, before and after

Server started with `TEAMUP_MINIO_ENDPOINT` pointed at a closed port, everything else live.

Before:

```
/readyz with MinIO at 127.0.0.1:38205: 200 {"status":"ready"}
/readyz with LiveKit at ws://127.0.0.1:37275: 200 {"status":"ready"}
```

After:

```
/readyz with MinIO at 127.0.0.1:38205: 503
{"status":"not ready","checks":{
  "database":{"status":"pass","latency":"0s"},
  "livekit":{"status":"pass","latency":"1ms"},
  "minio":{"status":"fail","message":"object storage at 127.0.0.1:38205 is unreachable: Get \"http://127.0.0.1:38205/teamup/?location=\": dial tcp 127.0.0.1:38205: connect: connection refused","latency":"1ms"},
  "redis":{"status":"pass","latency":"1ms"}},
 "timestamp":"...","version":"dev"}

/readyz with LiveKit at ws://127.0.0.1:37275: 503
{"status":"not ready","checks":{
  "livekit":{"status":"fail","message":"unreachable at http://127.0.0.1:37275: Get \"http://127.0.0.1:37275\": dial tcp ...: connection refused","latency":"1ms"},
  ...}}
```

`/healthz` returned 200 throughout both, which is the point of keeping them separate.

Healthy, all four up: `200 {"status":"ready","checks":{"database":{...,"latency":"0s"},"livekit":{...,"2ms"},"minio":{...,"4ms"},"redis":{...,"1ms"}},...}`, and 30 back-to-back probes averaged **488µs** each — the cache means a probe loop is not four dials.

### The fix

`internal/observability/readiness.go` (new) — a `Readiness` type over a list of `Dependency{Name, Check}`:

- checks run **concurrently** under one deadline (`WithProbeTimeout`, default **2s**);
- results are **cached** (`WithCacheTTL`, default **2s**), with the refresh under the lock so concurrent probes collapse onto one round rather than each starting their own;
- the caller's context bounds the probe from above but is detached with `context.WithoutCancel`, so a client that hangs up cannot poison the cached answer;
- the body is the existing `HealthResponse`/`CheckResult`, so every dependency is reported with `pass`/`fail`, a reason and a latency.

`observability.ReadyHandler(deps ...Dependency)` replaces the old two-argument form; `LiveHandler` is untouched and gained a comment saying why it must stay dependency-free. `Init` now records the build version so both health responses report it instead of the hardcoded `"dev"`.

`internal/files` gained `(*Service).Ping(ctx)` — `BucketExists` against the configured bucket, so it checks reachability, credentials *and* that the bucket is there, which is what an upload actually needs.

LiveKit is probed with a generic `observability.HTTPCheck` against the same target `liveKitProxyTarget` already derives for the `/livekit` reverse proxy (LiveKit answers `200 OK` on its HTTP root). It is registered only when `TEAMUP_LIVEKIT_URL` parses; a config without one is simply not gated on it. This avoids touching `internal/calls`.

### Probe contract — for the Helm chart

| | |
|---|---|
| **Liveness** | `GET /healthz` (alias `/healthz/live`). Always `200 {"status":"alive"}` while the process is up. Consults **nothing**. Never make this depend on a dependency. |
| **Readiness** | `GET /readyz` (alias `/healthz/ready`, same handler). `200` when every dependency passes, `503` when any fails. |

Readiness body, both cases:

```json
{"status":"ready"|"not ready",
 "checks":{"<name>":{"status":"pass"|"fail","message":"<only on fail>","latency":"4ms"}},
 "timestamp":"2026-08-11T02:36:14Z","version":"dev"}
```

Dependency names are exactly `database`, `redis`, `minio`, `livekit` (`livekit` present only when `TEAMUP_LIVEKIT_URL` is set to a parseable `ws`/`wss`/`http`/`https` URL).

Cost: results cached 2s, one round bounded at 2s, checks concurrent. So probing every 5s costs essentially nothing, and even `periodSeconds: 1` would be served from cache. Suggested:

```yaml
livenessProbe:
  httpGet: { path: /healthz, port: http }
  periodSeconds: 10
  failureThreshold: 3
readinessProbe:
  httpGet: { path: /readyz, port: http }
  periodSeconds: 5
  timeoutSeconds: 3      # must exceed the 2s probe timeout
  failureThreshold: 3
startupProbe:
  httpGet: { path: /readyz, port: http }
  periodSeconds: 5
  failureThreshold: 30   # MinIO/LiveKit may start after the app
```

Also from #38: **`TEAMUP_SHUTDOWN_GRACE`** (duration, default `30s`) must be set below `terminationGracePeriodSeconds` — e.g. grace `25s` against a termination period of `30s` — or SIGKILL cuts the drain short.

Note the process still hard-fails at boot if Postgres or Redis are unreachable (`connectRedis` pings and `runServer` returns an error). MinIO and LiveKit deliberately do not, per the note above: they gate readiness instead, so an app that starts before them comes up and joins the load balancer when they arrive rather than crash-looping.

### Tests

- `cmd/teamup/readiness_test.go` — drives the real binary: `/readyz` is 503 and names `minio` with a dead MinIO endpoint, 503 and names `livekit` with a dead LiveKit URL, `/healthz` is 200 in both, and with everything live `/readyz` is 200, reports all four, and averages under 10ms per probe over 30 probes.
- `internal/observability/readiness_test.go` — the contract in unit form: names the failing dependency and carries a reason; caches within its TTL (20 probes → 1 check); rechecks after the TTL so a recovered dependency is noticed; bounds a hanging check; runs checks concurrently (4 × 150ms in under 500ms); is not poisoned by a client hanging up; liveness consults nothing; `HTTPCheck` rejects both a 500 and a closed port.

### Deliberately not done

- The cache TTL and probe timeout are not configurable through the environment. Two seconds is right for a probe every 5s and there is nothing yet asking for another value; `WithCacheTTL`/`WithProbeTimeout` are there when there is.
- SMTP is not a readiness dependency. It is optional and asynchronous, and gating traffic on a mail server would be wrong.
