# Shipping TeamUp: deployable and scalable

Design for turning TeamUp from a codebase that runs on a developer's machine into
one that installs on someone else's server in a single command, and that can grow
into Kubernetes later without a rewrite.

## The problem

TeamUp already has a `Dockerfile`, a `docker-compose.prod.yml`, an `nginx.conf`,
a Helm chart and a `make helm` target. Four of those do not work.

| Claim | Reality |
| --- | --- |
| Helm chart | `helm/teamup/templates/` is empty. `helm install` reports success and creates no Deployment, Service or Ingress. |
| `docker build` | Fails on a clean clone. `main.go:49` embeds `web/dist` relative to `cmd/teamup/`, that directory is gitignored, and the Dockerfile never builds the frontend — it copies the artifact to `/app/web/dist`, the wrong path for that embed. |
| Published image | Both compose files and the chart reference `ghcr.io/yourorg/teamup:latest`. Nothing builds or pushes it. There is no CI at all. |
| Production migrations | `runMigrate` is wired only to the CLI subcommand. The container entrypoint is `["/teamup", "server"]`, and neither compose nor the chart ever runs `migrate up`. |
| App healthcheck | `docker-compose.prod.yml` uses `wget` against a distroless image, which has no shell and no wget. The check can never pass. |
| Horizontal scale | `internal/websocket/hub.go` holds connections in in-process maps. There is no Redis pub/sub anywhere in the Go code. Two replicas silently stop delivering messages to each other. |

Separately, `docker-compose.prod.yml` publishes Postgres 5432, Redis 6379 and
MinIO 9000/9001 to the host, and `values.yaml` ships `changeme` as the default
password for four services.

What does work and is kept: graceful SIGTERM shutdown, `/healthz` and `/readyz`,
a distroless non-root runtime, Prometheus `/metrics`, migrations as a proper
`migrate up|down|version|force` subcommand, and — decisively for this design — a
LiveKit signalling reverse proxy already implemented in Go at `main.go:528-548`.

## Decisions

1. **Appliance first, Kubernetes later.** Single-VM install is the deliverable.
   Architectural choices are made so the Kubernetes path is additive.
2. **Two install paths.** A one-command Docker Compose install with automatic
   TLS, and a static binary under systemd.
3. **All four services stay required.** Postgres, Redis, MinIO and LiveKit remain
   mandatory. No local-disk file backend, no in-process presence backend. The
   binary path documents its four dependencies rather than eliminating them.
4. **A seam for the hub, not an implementation.** Extract the fan-out interface
   now; write the Redis implementation in the Kubernetes phase.
5. **TLS terminates in the Go binary.** Because `/livekit` is already proxied in
   Go, a reverse proxy has no remaining job beyond TLS. Moving TLS into the
   binary lets both install paths run identical code.

## Architecture

### TLS and topology

`nginx.conf` is deleted; no reverse proxy remains in the stack.

`main.go` gains `TEAMUP_DOMAIN`:

- **Set** — serve HTTPS on 443 via `golang.org/x/crypto/acme/autocert`, with a
  redirect-only listener on 80.
- **Unset** — serve plain HTTP on `TEAMUP_PORT`, exactly as today. This keeps
  local development, `./start.sh --lan`, and the eventual
  Kubernetes-behind-an-Ingress case working unchanged.

The certificate cache is a directory, `TEAMUP_TLS_CACHE`, defaulting to
`/var/lib/teamup/certs`: a mounted volume under Compose, systemd's
`StateDirectory` on the binary path, and a `PersistentVolumeClaim` later. Note
for the Kubernetes phase: this is why `readOnlyRootFilesystem: true` will need a
writable mount.

`TEAMUP_BASE_URL` is derived from `TEAMUP_DOMAIN` when unset, instead of being
hand-set to `https://teamup.example.com`. The frontend's LiveKit signalling URL
defaults to the existing `/livekit` proxy path rather than being configured.

LiveKit's media ports are not proxied by anything and never were. UDP
50000–60000 must reach the LiveKit process directly — a security-group rule on a
VM, and `hostNetwork` or a wide NodePort range in Kubernetes.

### Build

Three Docker stages:

1. **Frontend** — `node:22-alpine`, `pnpm install --frozen-lockfile`, `pnpm build`.
   Vite's `outDir` is already `../cmd/teamup/web/dist`, so output lands exactly
   where `//go:embed all:web/dist` expects it.
2. **Go** — `go mod download` against the committed `go.sum`, then
   `COPY --from=frontend`, then build. `go mod tidy` is removed from the build:
   it requires network access and makes builds non-reproducible. `GOARCH`
   becomes `$TARGETARCH` for multi-arch buildx. Version ldflags arrive as build
   args instead of the hardcoded `-X main.version=dev`.
3. **Runtime** — distroless static, non-root, unchanged.

`ENTRYPOINT ["/teamup"]` with `CMD ["server"]` replaces
`ENTRYPOINT ["/teamup", "server"]`, so the same image can run `migrate up`.

A new `.dockerignore` excludes `node_modules` (310MB), the committed `teamup`
binary (53MB), `.git`, `.env`, `certs/`, `.scratch/` and `.serena/`.

A new `teamup healthcheck` subcommand dials `/readyz` in-process. It replaces the
`wget` healthcheck, and works on distroless precisely because it is the same
binary.

### Configuration and secrets

Compose services: `postgres`, `redis`, `minio`, `livekit`, `teamup-migrate` (one
shot, `command: ["migrate","up"]`, gated on Postgres healthy) and `teamup` (gated
on the migrate job's `service_completed_successfully`). This closes the gap where
nothing in production ran migrations.

Every port publication is removed except the app's 80/443 and LiveKit's RTC
range. Postgres, Redis and MinIO become internal-only. The obsolete
`version: '3.8'` key is removed.

Secrets lose their defaults. `make secrets` generates `POSTGRES_PASSWORD`,
`REDIS_PASSWORD`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `LIVEKIT_API_KEY`,
`LIVEKIT_API_SECRET`, `AUTH_SECRET` and the VAPID keypair with `openssl rand`,
writes `.env`, and refuses to overwrite an existing one. The server refuses to
boot when `TEAMUP_ENV=production` and `AUTH_SECRET` is empty.

### The fan-out seam

The three broadcast methods on `Hub` keep their signatures. Each body splits:
the map-walking half becomes an unexported `deliverLocal`, unchanged; the public
half calls a new interface.

```go
type Fanout interface {
    Publish(scope Scope, msg *protocol.Message, excludeUserID string)
}
```

`localFanout` is the only implementation in this phase and calls `deliverLocal`
directly, so behaviour is byte-identical. The Redis implementation later
publishes to a channel, and each instance's subscriber calls `deliverLocal` on
receipt — a new file and a constructor change, with no surgery inside `hub.go`.

On boot the server logs once that fan-out is single-instance and replicas must
remain at 1.

### The Helm chart

`helm/` and the `make helm` target are deleted. A chart whose `templates/`
directory is empty does not fail — it reports success and deploys nothing, which
is a worse failure than shipping no chart. It returns in the Kubernetes phase,
alongside the Redis fan-out, when the values it advertises can be honoured.

## Install experience

### Compose, no repo clone

```
curl -fsSLO https://github.com/<owner>/teamup/releases/latest/download/compose.yml
curl -fsSL  https://github.com/<owner>/teamup/releases/latest/download/gen-env.sh | sh -s teamup.acme.com
docker compose up -d
```

`gen-env.sh` is `make secrets` in portable form: it generates every credential,
sets `TEAMUP_DOMAIN`, and refuses to clobber an existing `.env`. First boot
fetches a certificate, runs migrations through the one-shot service, and the
existing wizard takes over at `https://teamup.acme.com`. Prerequisites are a DNS
A record and open ports 80, 443 and UDP 50000–60000.

### Static binary under systemd

A binary from the GitHub Release, plus `scripts/install-systemd.sh`, which
creates a `teamup` system user, writes `/etc/teamup/teamup.env` with generated
secrets, and installs:

```ini
[Service]
Type=exec
User=teamup
EnvironmentFile=/etc/teamup/teamup.env
ExecStartPre=/usr/local/bin/teamup migrate up
ExecStart=/usr/local/bin/teamup server
AmbientCapabilities=CAP_NET_BIND_SERVICE
StateDirectory=teamup
ProtectSystem=strict
NoNewPrivileges=true
Restart=on-failure
```

`AmbientCapabilities` lets a non-root process bind 443. `StateDirectory=teamup`
provisions `/var/lib/teamup` with correct ownership, which is the
`TEAMUP_TLS_CACHE` default. `ExecStartPre` gives this path the same
migrate-before-serve guarantee the Compose path gets from its one-shot service.

`Type=exec` is chosen over `Type=notify` deliberately: `notify` would require
adding an `sd_notify` call and a systemd dependency to the server for no benefit
here, since nothing downstream waits on the unit's readiness. `/readyz` remains
the readiness signal for anything that needs one.

A second script, `install-services.sh`, installs Postgres and Redis from the
distribution's package manager and MinIO and LiveKit from their upstream release
binaries, writes a systemd unit for each, and records their connection strings in
`/etc/teamup/teamup.env`. It supports Debian and Ubuntu only; other
distributions are directed to the Compose path. `DEPLOYMENT.md` states plainly
that this path installs four services and is not lighter than Docker — it exists
for operators who will not run containers.

### Documentation

`DEPLOYMENT.md` is restructured around four paths: local development, LAN
testing, Compose production, binary production. The secure-context explanation
at the top is kept — it is what explains why the LAN path has no calls and no
desktop notifications. The manual certificate ceremony is removed, since
autocert replaces it.

## CI/CD

Assumes GitHub Actions and GHCR, consistent with the `ghcr.io` references
already in the repository. No git remote is currently configured; this assumption
must be confirmed before the workflows are written.

- **`ci.yml`** (push, pull request): `golangci-lint`; `go test -race` with a
  Postgres service container wired to the existing `TEAMUP_TEST_DATABASE_URL`
  contract; frontend lint and build; and `docker build` without push. The last
  step is what makes the clean-clone build failure impossible to reintroduce.
- **`release.yml`** (tag `v*`): multi-arch buildx to
  `ghcr.io/<owner>/teamup:{version,latest}` for amd64 and arm64; static linux
  amd64 and arm64 binaries, `compose.yml` and `gen-env.sh` attached to the
  GitHub Release.

## Verification

Most of the defects above exist because the code was never once executed in the
shape it claims to support. The verification plan is therefore mostly execution.

- Docker image builds from a clean checkout on every pull request.
- A `smoke` job brings up the real Compose stack with `TEAMUP_DOMAIN` unset
  (HTTP, no ACME), waits on `/readyz`, provisions an org and admin through the
  existing `TEAMUP_AUTO_SETUP` variables, then signs in and posts a message over
  both the REST API and the WebSocket. This is the executable form of
  "deployable".
- `teamup healthcheck` is covered by the Compose healthcheck passing in the
  smoke job — a distroless-specific failure that only appears when run.
- The autocert branch cannot be exercised in CI without a real domain. It gets
  unit coverage on the config decision (domain set versus unset selects the
  right listener), plus a manual checklist in `DEPLOYMENT.md`.
- The fan-out seam is verified by the existing `hub_test.go` suite continuing to
  pass unchanged, since `localFanout` must be behaviour-identical.

## Out of scope

- **The Redis fan-out implementation.** Deferred to the Kubernetes phase by
  decision 4. Until it lands, TeamUp runs at exactly one replica.
- **The Helm chart.** Deleted here, rewritten in the Kubernetes phase.
- **Backup and restore** for Postgres and MinIO. A real gap for a self-hosted
  appliance, and its own design.
- **Multi-tenant SaaS concerns** — tenant isolation, per-tenant limits,
  zero-downtime deploys.
