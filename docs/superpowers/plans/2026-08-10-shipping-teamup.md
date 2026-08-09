# Shipping TeamUp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make TeamUp installable by someone who did not write it — a Docker image that builds from a clean clone, CI that publishes it, automatic TLS, a one-command Compose install, a systemd install, and a fan-out seam so Kubernetes is additive later.

**Architecture:** TLS terminates inside the Go binary via `autocert`, driven by `TEAMUP_DOMAIN`. The server already reverse-proxies `/livekit` (`cmd/teamup/main.go:528-548`), so no reverse proxy remains in the stack and both install paths run identical code. The in-memory WebSocket hub gains a `Fanout` interface whose only implementation delivers locally; the Redis implementation is deliberately out of scope.

**Tech Stack:** Go 1.25, React + Vite + TypeScript, PostgreSQL 16, Redis 7, MinIO (S3-compatible), LiveKit, Docker Compose v2, GitHub Actions, distroless runtime.

**Spec:** `docs/superpowers/specs/2026-08-10-shipping-teamup-design.md`

## Global Constraints

- Go **1.25.0** (`go.mod`). Node **20+**. pnpm **10.28.2** (lockfile is `lockfileVersion: '9.0'`).
- **No new Go module dependencies.** `golang.org/x/crypto v0.37.0` is already a direct dependency and provides `acme/autocert`.
- Runtime image stays `gcr.io/distroless/static-debian12:nonroot`. It has **no shell and no wget** — anything that runs inside the container must be the `teamup` binary.
- **All four backing services stay required**: PostgreSQL, Redis, MinIO, LiveKit. Do not add a local-disk file backend or an in-process presence backend.
- **Single replica only.** The Redis fan-out is out of scope; do not implement it.
- Frontend build output path is fixed by `web/vite.config.ts:65` (`outDir: '../cmd/teamup/web/dist'`) and must match `//go:embed all:web/dist` in `cmd/teamup/main.go:49`.
- Commit messages use conventional-commit prefixes (`feat:`, `fix:`, `docs:`, `ci:`, `build:`, `chore:`) with a subject saying what changed for the user.
- Comments explain **why**, not what — match the existing style in `internal/websocket/hub.go` and `start.sh`.
- `pre-commit` runs on every commit (gofmt, `go build`, `go vet`, `go test`, `tsc --noEmit`, eslint). All must pass.

---

## File Structure

**Created:**

| Path | Responsibility |
| --- | --- |
| `.dockerignore` | Keeps `node_modules`, the committed binary and secrets out of build context |
| `cmd/teamup/healthcheck.go` | `teamup healthcheck` — readiness probe that works on distroless |
| `cmd/teamup/healthcheck_test.go` | Tests for the above |
| `cmd/teamup/serve.go` | Listener selection (plain HTTP vs autocert), base-URL derivation, production config validation |
| `cmd/teamup/serve_test.go` | Tests for the above |
| `internal/websocket/fanout.go` | `Fanout` interface, `Scope`, `localFanout` |
| `internal/websocket/fanout_test.go` | Proves broadcasts route through the seam |
| `scripts/gen-env.sh` | Generates `.env` with random secrets (portable, no `make`) |
| `scripts/install-systemd.sh` | Installs the binary, user, env file and unit |
| `scripts/install-services.sh` | Installs PostgreSQL, Redis, MinIO, LiveKit on Debian/Ubuntu |
| `.github/workflows/ci.yml` | Lint, test, clean-clone image build |
| `.github/workflows/smoke.yml` | Boots the real stack and exercises it |
| `.github/workflows/release.yml` | Multi-arch image to GHCR + release artifacts |

**Modified:**

| Path | Change |
| --- | --- |
| `Dockerfile` | Three stages; frontend built inside; `TARGETARCH`; `ENTRYPOINT`/`CMD` split |
| `cmd/teamup/main.go` | `healthcheck` command; `Domain`/`TLSCacheDir` config; call `serve.go` helpers |
| `internal/websocket/hub.go` | Broadcast bodies move to `deliverLocal`; public methods call `fanout` |
| `docker-compose.prod.yml` | Migrate one-shot; no published database ports; no nginx; TLS volume |
| `web/package.json` | Add `packageManager` field so corepack pins pnpm |
| `Makefile` | Add `secrets`; remove `helm` |
| `.env.example` | Add `TEAMUP_DOMAIN`, `TEAMUP_TLS_CACHE` |
| `docs/DEPLOYMENT.md` | Restructure around four paths |
| `README.md` | Replace the "production path is being reworked" note |

**Deleted:** `nginx.conf`, `helm/`

---

## Task 1: A Docker image that builds from a clean clone

Today `docker build` succeeds only on a machine that has already run `make build`. `//go:embed all:web/dist` resolves relative to `cmd/teamup/`, `cmd/teamup/web/dist` is gitignored, and the Dockerfile copies the artifact to `/app/web/dist` — the wrong path. The build also ships 310MB of `node_modules` and a 53MB committed binary into context, and runs `go mod tidy`, which needs network access.

**Files:**
- Create: `.dockerignore`
- Modify: `Dockerfile` (full rewrite), `web/package.json`

**Interfaces:**
- Consumes: nothing.
- Produces: an image whose entrypoint is `/teamup` with default arg `server`, so `docker run <img> migrate up` and `docker run <img> healthcheck` work. Task 6 and Task 9 depend on this.

- [ ] **Step 1: Pin pnpm so corepack can install it in the image**

`web/package.json` has no `packageManager` field, so `corepack enable` has nothing to pin to. Add it as a sibling of `"scripts"` (the local pnpm is 10.28.2 and the lockfile is `lockfileVersion: '9.0'`):

```json
  "packageManager": "pnpm@10.28.2",
```

- [ ] **Step 2: Write `.dockerignore`**

Excluding `cmd/teamup/web/dist` is deliberate: it guarantees the frontend stage is the only possible source of the embedded assets, so a stale local build can never silently satisfy the embed.

```
.git
.github
.claude
.agents
.serena
.scratch
node_modules
web/node_modules
web/dist
cmd/teamup/web/dist
web/test-results
web/playwright-report
bin/
teamup
*.test
coverage.out
.env
.env.*
!.env.example
certs/
*.pem
*.key
*.crt
docs/
```

- [ ] **Step 3: Rewrite the Dockerfile**

```dockerfile
# syntax=docker/dockerfile:1

# The web app. Vite writes to ../cmd/teamup/web/dist (web/vite.config.ts:65),
# so building from /app/web lands the output at /app/cmd/teamup/web/dist —
# exactly where //go:embed all:web/dist looks. Building it here rather than
# expecting it on the host is what makes a clean clone buildable.
FROM node:20-alpine AS frontend
WORKDIR /app/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

# The server. go mod tidy is deliberately absent: it needs network access and
# can change go.mod mid-build, so the image would not match the committed
# dependency set.
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/
COPY migrations/ ./migrations/
COPY --from=frontend /app/cmd/teamup/web/dist ./cmd/teamup/web/dist

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /teamup ./cmd/teamup

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /teamup /teamup
COPY --from=builder /app/migrations /migrations

EXPOSE 8080
USER nonroot:nonroot

# Split so the same image can run `migrate up` and `healthcheck`, not just the
# server. The migrate one-shot in docker-compose.prod.yml depends on this.
ENTRYPOINT ["/teamup"]
CMD ["server"]
```

- [ ] **Step 4: Prove it builds from committed content only**

This is the regression test for the embed bug. `git archive` exports only what is committed, so a stale local `cmd/teamup/web/dist` cannot participate.

```bash
rm -rf /tmp/teamup-cleanbuild && mkdir -p /tmp/teamup-cleanbuild
git stash list >/dev/null  # no-op; ensures you are in a git repo
git archive HEAD | tar -x -C /tmp/teamup-cleanbuild
docker build -t teamup:cleanbuild /tmp/teamup-cleanbuild
```

Expected: build succeeds. Before Step 3 it fails in the Go stage with `pattern all:web/dist: no matching files found`.

> If Step 1's `packageManager` change is not yet committed, `git archive HEAD` will not contain it and the frontend stage fails at `pnpm install`. Commit Step 1 and Step 2 first, or use `git stash` + a dirty-tree copy instead.

- [ ] **Step 5: Verify the entrypoint split and the embedded assets**

```bash
docker run --rm teamup:cleanbuild version
```
Expected: prints `TeamUp dev`, `Commit: unknown`, `Built: unknown` — proving `ENTRYPOINT`/`CMD` accepts a subcommand.

```bash
docker run --rm --entrypoint /teamup teamup:cleanbuild migrate 2>&1 | head -3
```
Expected: a cobra usage or config error about `TEAMUP_DATABASE_URL`, **not** "unknown command".

- [ ] **Step 6: Commit**

```bash
git add .dockerignore Dockerfile web/package.json
git commit -m "build: make the image build from a clean clone

The Dockerfile expected cmd/teamup/web/dist to already exist on the host
and copied it to the wrong path for the embed, so docker build only ever
worked on a machine that had run make build. The web app is now a build
stage. Drops go mod tidy from the build, adds .dockerignore, and splits
ENTRYPOINT from CMD so the image can also run migrate and healthcheck."
```

---

## Task 2: A healthcheck that works on distroless

`docker-compose.prod.yml` healthchecks the app with `wget`. The runtime image is distroless and has no wget and no shell, so that check can never pass — and anything using `depends_on: service_healthy` against it would wait forever.

**Files:**
- Create: `cmd/teamup/healthcheck.go`, `cmd/teamup/healthcheck_test.go`
- Modify: `cmd/teamup/main.go:306` (command registration)

**Interfaces:**
- Consumes: nothing.
- Produces: `teamup healthcheck`, exit 0 when ready. Task 6 uses it as the Compose healthcheck.

- [ ] **Step 1: Write the failing test**

Create `cmd/teamup/healthcheck_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckAcceptsAReadyServer(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := checkReadiness(srv.URL); err != nil {
		t.Fatalf("a ready server was rejected: %v", err)
	}
	if asked != "/readyz" {
		t.Errorf("asked for %q, want /readyz", asked)
	}
}

func TestHealthcheckRejectsAServerThatIsNotReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := checkReadiness(srv.URL); err == nil {
		t.Fatal("a 503 was accepted as ready")
	}
}

func TestHealthcheckRejectsAServerThatIsNotListening(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	if err := checkReadiness(url); err == nil {
		t.Fatal("a closed port was accepted as ready")
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

```bash
go test ./cmd/teamup/ -run TestHealthcheck -v
```
Expected: FAIL — `undefined: checkReadiness`.

- [ ] **Step 3: Write the implementation**

Create `cmd/teamup/healthcheck.go`:

```go
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// checkReadiness asks a running server whether it is ready to serve.
func checkReadiness(baseURL string) error {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(baseURL + "/readyz")
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: /readyz returned %d", resp.StatusCode)
	}
	return nil
}

// runHealthcheck exists because the runtime image is distroless: it has no
// shell and no wget, so a container healthcheck cannot be a shell command. The
// binary is the only executable in the image, so it has to check itself.
//
// It always talks to the plain-HTTP port on loopback, even when the server is
// serving HTTPS to the outside world — the certificate is issued for the public
// domain and would not validate against 127.0.0.1.
func runHealthcheck(cmd *cobra.Command, args []string) error {
	port := os.Getenv("TEAMUP_PORT")
	if port == "" {
		port = "8080"
	}
	return checkReadiness("http://127.0.0.1:" + port)
}
```

- [ ] **Step 4: Register the command**

In `cmd/teamup/main.go`, after the `wizardCmd` block (currently ending at line 304), add:

```go
	// Healthcheck command
	healthcheckCmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 if this server is ready to serve",
		RunE:  runHealthcheck,
	}
```

and change line 306 from:

```go
	rootCmd.AddCommand(serverCmd, migrateCmd, versionCmd, wizardCmd)
```

to:

```go
	rootCmd.AddCommand(serverCmd, migrateCmd, versionCmd, wizardCmd, healthcheckCmd)
```

- [ ] **Step 5: Run the tests and confirm they pass**

```bash
go test ./cmd/teamup/ -run TestHealthcheck -v
```
Expected: three PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/teamup/healthcheck.go cmd/teamup/healthcheck_test.go cmd/teamup/main.go
git commit -m "feat: let the container check its own health

The compose healthcheck ran wget against a distroless image, which has
no shell and no wget, so it could never pass and anything waiting on
service_healthy would have waited forever. The binary now checks itself."
```

---

## Task 3: A fan-out seam in the WebSocket hub

`internal/websocket/hub.go` holds every connection in process-local maps and there is no Redis pub/sub anywhere in the Go code, so a second replica silently stops delivering. This task does **not** fix that. It extracts the seam so the Redis implementation is a new file rather than surgery, and makes the limitation visible at boot.

**Files:**
- Create: `internal/websocket/fanout.go`, `internal/websocket/fanout_test.go`
- Modify: `internal/websocket/hub.go:22-35` (struct), `:63-79` (`NewHub`), `:206-271` (the three broadcast methods)

**Interfaces:**
- Consumes: nothing.
- Produces: `websocket.Fanout` (interface with `Publish(Scope, *protocol.Message)`), `websocket.Scope{Kind ScopeKind, ID string, ExcludeUserID string}`, `ScopeKind` constants `ScopeChannel`/`ScopeUser`/`ScopeOrg`, and `(*Hub).SetFanout(Fanout)`. No other task consumes these; the Redis implementation will.

- [ ] **Step 1: Write the failing test**

Create `internal/websocket/fanout_test.go`. This asserts the seam is real — that swapping the implementation actually redirects delivery, which is the whole point of the task.

```go
package websocket

import (
	"testing"

	"github.com/teamup/teamup/pkg/protocol"
)

// recordingFanout stands in for the Redis implementation: it captures what was
// published instead of delivering it.
type recordingFanout struct {
	scopes []Scope
	msgs   []*protocol.Message
}

func (f *recordingFanout) Publish(scope Scope, msg *protocol.Message) {
	f.scopes = append(f.scopes, scope)
	f.msgs = append(f.msgs, msg)
}

func TestBroadcastToUserGoesThroughTheFanout(t *testing.T) {
	hub := newTestHub(t)
	rec := &recordingFanout{}
	hub.SetFanout(rec)

	msg := protocol.NewMessage(protocol.TypeNotificationNew, nil)
	hub.BroadcastToUser("user-1", msg)

	if len(rec.scopes) != 1 {
		t.Fatalf("published %d times, want 1", len(rec.scopes))
	}
	if rec.scopes[0].Kind != ScopeUser || rec.scopes[0].ID != "user-1" {
		t.Errorf("published %+v, want kind=%s id=user-1", rec.scopes[0], ScopeUser)
	}
	if rec.msgs[0] != msg {
		t.Error("a different message was published than the one broadcast")
	}
}

func TestBroadcastToChannelGoesThroughTheFanout(t *testing.T) {
	hub := newTestHub(t)
	rec := &recordingFanout{}
	hub.SetFanout(rec)

	hub.BroadcastToChannel("chan-1", protocol.NewMessage(protocol.TypeChatMessage, nil), "user-9")

	if len(rec.scopes) != 1 {
		t.Fatalf("published %d times, want 1", len(rec.scopes))
	}
	got := rec.scopes[0]
	if got.Kind != ScopeChannel || got.ID != "chan-1" || got.ExcludeUserID != "user-9" {
		t.Errorf("published %+v, want kind=%s id=chan-1 exclude=user-9", got, ScopeChannel)
	}
}

func TestBroadcastToOrgGoesThroughTheFanout(t *testing.T) {
	hub := newTestHub(t)
	rec := &recordingFanout{}
	hub.SetFanout(rec)

	hub.BroadcastToOrg("org-1", protocol.NewMessage(protocol.TypePresenceChanged, nil), "user-9")

	if len(rec.scopes) != 1 {
		t.Fatalf("published %d times, want 1", len(rec.scopes))
	}
	got := rec.scopes[0]
	if got.Kind != ScopeOrg || got.ID != "org-1" || got.ExcludeUserID != "user-9" {
		t.Errorf("published %+v, want kind=%s id=org-1 exclude=user-9", got, ScopeOrg)
	}
}
```

There is no `newTestHub` helper today — `hub_test.go` constructs hubs inline as `NewHub(nil, stubPresence{}, nil, zerolog.Nop())` at nine call sites. Passing `nil` for the presence service panics, so the helper must use the existing `stubPresence`, which lives in `hub_test.go` and is reachable because these files share the `websocket` package. Add to `fanout_test.go`, importing `"github.com/rs/zerolog"`:

```go
func newTestHub(t *testing.T) *Hub {
	t.Helper()
	return NewHub(nil, stubPresence{}, nil, zerolog.Nop())
}
```

Do not refactor the nine existing call sites to use it — that would make the diff larger than the change and obscure whether the existing tests still pass untouched, which is this task's main evidence.

- [ ] **Step 2: Run it and confirm it fails**

```bash
go test ./internal/websocket/ -run Fanout -v
```
Expected: FAIL — `undefined: Scope`, `undefined: ScopeUser`, `hub.SetFanout undefined`.

- [ ] **Step 3: Write `fanout.go`**

```go
package websocket

import "github.com/teamup/teamup/pkg/protocol"

// ScopeKind names the kind of audience a broadcast is aimed at.
type ScopeKind string

const (
	ScopeChannel ScopeKind = "channel"
	ScopeUser    ScopeKind = "user"
	ScopeOrg     ScopeKind = "org"
)

// Scope describes who should receive a message, in terms that survive leaving
// this process — ids and a kind, no pointers to connections.
type Scope struct {
	Kind ScopeKind
	// ID is a channel id, a user id or an org id, according to Kind.
	ID string
	// ExcludeUserID is the sender, who should not be told about their own
	// message. Unused for ScopeUser.
	ExcludeUserID string
}

// Fanout decides where a broadcast goes before delivery happens.
//
// It exists so that delivery can be handed to something other than this
// process without changing a single caller. Today localFanout hands straight
// back to the hub. A Redis implementation would publish the scope and message
// to a channel every instance subscribes to, and each instance would turn that
// back into deliverLocal on receipt — which is what makes more than one replica
// safe. Until that exists, TeamUp must run exactly one instance: a message sent
// on one would never reach sockets held by another.
type Fanout interface {
	Publish(scope Scope, msg *protocol.Message)
}

// localFanout delivers only to connections held by this process.
type localFanout struct{ hub *Hub }

func (f *localFanout) Publish(scope Scope, msg *protocol.Message) {
	f.hub.deliverLocal(scope, msg)
}
```

- [ ] **Step 4: Add the field, the setter, and the default**

In `internal/websocket/hub.go`, add to the `Hub` struct (after the `calls` field, around line 29):

```go
	fanout          Fanout
```

Replace the `return &Hub{...}` in `NewHub` (lines 70-79) with a two-step construction, because `localFanout` needs the hub it belongs to:

```go
	h := &Hub{
		services:        services,
		presenceService: presenceService,
		authService:     authService,
		logger:          logger,
		connections:     make(map[string]*Connection),
		userConns:       make(map[string]map[string]*Connection),
		channelSubs:     make(map[string]map[string]*Connection),
	}
	h.fanout = &localFanout{hub: h}
	return h
```

Add the setter next to the existing ones (near `SetChannelRoster`, line 170):

```go
// SetFanout replaces how broadcasts are distributed. The default delivers only
// within this process.
func (h *Hub) SetFanout(f Fanout) { h.fanout = f }
```

- [ ] **Step 5: Split the three broadcast methods**

Replace `BroadcastToChannel`, `BroadcastToUser` and `BroadcastToOrg` (lines 206-271) with thin publishers plus one local deliverer. The comment currently above `BroadcastToChannel` (lines 200-205) explains per-broadcast roster resolution and must move with the code that does it, onto `deliverToChannel`.

```go
func (h *Hub) BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string) {
	h.fanout.Publish(Scope{Kind: ScopeChannel, ID: channelID, ExcludeUserID: excludeUserID}, msg)
}

func (h *Hub) BroadcastToUser(userID string, msg *protocol.Message) {
	h.fanout.Publish(Scope{Kind: ScopeUser, ID: userID}, msg)
}

func (h *Hub) BroadcastToOrg(orgID string, msg *protocol.Message, excludeUserID string) {
	h.fanout.Publish(Scope{Kind: ScopeOrg, ID: orgID, ExcludeUserID: excludeUserID}, msg)
}

// deliverLocal sends a message to the connections this process holds. A Redis
// Fanout calls this on every instance; localFanout calls it on one.
func (h *Hub) deliverLocal(scope Scope, msg *protocol.Message) {
	switch scope.Kind {
	case ScopeChannel:
		h.deliverToChannel(scope.ID, msg, scope.ExcludeUserID)
	case ScopeUser:
		h.deliverToUser(scope.ID, msg)
	case ScopeOrg:
		h.deliverToOrg(scope.ID, msg, scope.ExcludeUserID)
	default:
		h.logger.Warn().Str("kind", string(scope.Kind)).Msg("dropping a broadcast with an unknown scope")
	}
}

// The roster is read at send time. This used to go through a subscription table
// that nothing ever filled in — subscribeToUserChannels was a stub — so channel
// messages reached nobody and the only way to see a new one was to reload the
// page. Resolving membership per broadcast also means a channel somebody joins,
// or a conversation started with them, works immediately without anybody having
// to reconnect.
func (h *Hub) deliverToChannel(channelID string, msg *protocol.Message, excludeUserID string) {
	recipients := map[string]bool{}

	if h.roster != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		members, err := h.roster.ChannelMemberIDs(ctx, channelID)
		if err != nil {
			h.logger.Warn().Err(err).Str("channel", channelID).Msg("could not resolve channel members for delivery")
		}
		for _, id := range members {
			recipients[id] = true
		}
	}

	// Anybody who explicitly subscribed is included too, so an explicit
	// subscription still works for clients that use one.
	h.mu.RLock()
	for userID := range h.channelSubs[channelID] {
		recipients[userID] = true
	}
	conns := make([]*Connection, 0, len(recipients))
	for userID := range recipients {
		if userID == excludeUserID {
			continue
		}
		for _, conn := range h.userConns[userID] {
			conns = append(conns, conn)
		}
	}
	h.mu.RUnlock()

	for _, conn := range conns {
		conn.Enqueue(msg)
	}
}

func (h *Hub) deliverToUser(userID string, msg *protocol.Message) {
	h.mu.RLock()
	userConns, ok := h.userConns[userID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	for _, conn := range userConns {
		conn.Enqueue(msg)
	}
}

func (h *Hub) deliverToOrg(orgID string, msg *protocol.Message, excludeUserID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for userID, userConns := range h.userConns {
		if userID == excludeUserID {
			continue
		}
		for _, conn := range userConns {
			if conn.OrgID() == orgID {
				conn.Enqueue(msg)
			}
		}
	}
}
```

Leave `broadcastPresence` (line 279) alone — it already walks connections directly and is not part of this seam.

- [ ] **Step 6: Run the new tests and the whole existing suite**

```bash
go test ./internal/websocket/ -run Fanout -v
go test ./internal/websocket/ -v
```

Expected: the three new tests PASS, and **all nine existing tests in `hub_test.go` pass unchanged** — `TestAChannelMessageReachesItsMembers` and `TestDeliveryFollowsMembershipWithoutReconnecting` in particular, since they are what prove `localFanout` is behaviour-identical. If any existing test needed editing, the refactor changed behaviour and is wrong.

- [ ] **Step 7: Say the limitation out loud at boot**

In `cmd/teamup/main.go`, immediately after the hub is constructed, add:

```go
	logger.Info().Msg("WebSocket fan-out is in-process; run exactly one instance of TeamUp")
```

- [ ] **Step 8: Commit**

```bash
git add internal/websocket/fanout.go internal/websocket/fanout_test.go internal/websocket/hub.go cmd/teamup/main.go
git commit -m "refactor: put a seam where cross-instance delivery will go

The hub holds every connection in process-local maps, so a second
replica silently stops delivering messages to the first. This does not
fix that. It moves delivery behind a Fanout interface so the Redis
implementation is a new file rather than surgery on the hub, and says
at boot that only one instance may run."
```

---

## Task 4: Automatic TLS in the binary

The server already reverse-proxies `/livekit` (`cmd/teamup/main.go:528-548`), so a reverse proxy exists only to terminate TLS. Moving TLS into the binary lets the Compose install and the systemd install run identical code, and deletes the manual certificate ceremony from the docs.

**Files:**
- Create: `cmd/teamup/serve.go`, `cmd/teamup/serve_test.go`
- Modify: `cmd/teamup/main.go` (Config struct ~line 200, `runServer` ~line 313, listen call at line 621-624), `.env.example`

**Interfaces:**
- Consumes: nothing.
- Produces: `resolveServeMode(Config) serveMode`, `resolveBaseURL(Config) string`, `listenAndServe(*http.Server, Config, zerolog.Logger) error`, and config fields `Config.Domain` / `Config.TLSCacheDir`. Task 6 sets `TEAMUP_DOMAIN`; Task 8 sets `TEAMUP_TLS_CACHE`.

- [ ] **Step 1: Write the failing test**

Create `cmd/teamup/serve_test.go`:

```go
package main

import "testing"

func TestServeModeIsPlainHTTPWithoutADomain(t *testing.T) {
	got := resolveServeMode(Config{Port: "8080"})

	if got.TLS {
		t.Error("TLS was enabled with no domain configured")
	}
	if got.Addr != ":8080" {
		t.Errorf("listening on %q, want :8080", got.Addr)
	}
}

func TestServeModeIsHTTPSWithADomain(t *testing.T) {
	got := resolveServeMode(Config{Port: "8080", Domain: "teamup.example.com"})

	if !got.TLS {
		t.Error("a domain was configured but TLS was not enabled")
	}
	if got.Addr != ":443" {
		t.Errorf("listening on %q, want :443", got.Addr)
	}
}

func TestBaseURLIsLeftAloneWhenSet(t *testing.T) {
	cfg := Config{Port: "8080", Domain: "teamup.example.com", BaseURL: "https://chat.example.com"}

	if got := resolveBaseURL(cfg); got != "https://chat.example.com" {
		t.Errorf("base URL %q, want the configured https://chat.example.com", got)
	}
}

func TestBaseURLFollowsTheDomain(t *testing.T) {
	cfg := Config{Port: "8080", Domain: "teamup.example.com"}

	if got := resolveBaseURL(cfg); got != "https://teamup.example.com" {
		t.Errorf("base URL %q, want https://teamup.example.com", got)
	}
}

func TestBaseURLFallsBackToLocalhost(t *testing.T) {
	cfg := Config{Port: "9000"}

	if got := resolveBaseURL(cfg); got != "http://localhost:9000" {
		t.Errorf("base URL %q, want http://localhost:9000", got)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

```bash
go test ./cmd/teamup/ -run 'ServeMode|BaseURL' -v
```
Expected: FAIL — `undefined: resolveServeMode`, `undefined: resolveBaseURL`.

- [ ] **Step 3: Add the config fields**

In `cmd/teamup/main.go`, in the `Config` struct under `// Server`:

```go
	// Domain switches on HTTPS with an automatically issued certificate. Left
	// empty the server serves plain HTTP, which is what local development,
	// ./start.sh --lan, and running behind a TLS-terminating load balancer all
	// need.
	Domain      string `env:"TEAMUP_DOMAIN"`
	TLSCacheDir string `env:"TEAMUP_TLS_CACHE" envDefault:"/var/lib/teamup/certs"`
```

Change the existing `BaseURL` field to drop its default, so "unset" is detectable:

```go
	// Derived from Domain when unset — see resolveBaseURL.
	BaseURL string `env:"TEAMUP_BASE_URL"`
```

- [ ] **Step 4: Write `serve.go`**

```go
package main

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/crypto/acme/autocert"
)

type serveMode struct {
	TLS  bool
	Addr string
}

// resolveServeMode decides how the server listens. A configured domain means
// TeamUp terminates TLS itself on the standard port; without one it stays on
// plain HTTP wherever it was told to.
func resolveServeMode(cfg Config) serveMode {
	if cfg.Domain == "" {
		return serveMode{TLS: false, Addr: ":" + cfg.Port}
	}
	return serveMode{TLS: true, Addr: ":443"}
}

// resolveBaseURL fills in TEAMUP_BASE_URL when it was not set.
//
// It is derived rather than defaulted because the old default of
// http://localhost:8080 was silently wrong on any real deployment: OIDC
// redirect URIs and the links in notification emails would point at the
// server's own loopback address, and the failure only shows up at sign-in.
func resolveBaseURL(cfg Config) string {
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	if cfg.Domain != "" {
		return "https://" + cfg.Domain
	}
	return "http://localhost:" + cfg.Port
}

// listenAndServe starts the server, obtaining a certificate first when a domain
// is configured.
//
// The cache directory matters more than it looks: without a persistent one,
// every restart asks Let's Encrypt for a fresh certificate and a few restarts
// are enough to hit the rate limit, which locks the domain out for a week.
//
// The listener on :80 is not decoration. It serves the HTTP-01 challenge and
// redirects everything else, so a visitor typing the bare hostname arrives on
// HTTPS rather than a refused connection.
func listenAndServe(srv *http.Server, cfg Config, logger zerolog.Logger) error {
	mode := resolveServeMode(cfg)
	srv.Addr = mode.Addr

	if !mode.TLS {
		logger.Info().Str("addr", srv.Addr).Msg("serving plain HTTP")
		return srv.ListenAndServe()
	}

	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Domain),
		Cache:      autocert.DirCache(cfg.TLSCacheDir),
	}
	srv.TLSConfig = manager.TLSConfig()

	go func() {
		redirect := &http.Server{
			Addr:         ":80",
			Handler:      manager.HTTPHandler(nil),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		if err := redirect.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("the HTTP redirect listener stopped")
		}
	}()

	// A plain-HTTP listener bound to loopback, so `teamup healthcheck` has
	// something to talk to. Without it the container healthcheck can never pass
	// in TLS mode: the only other listener speaks TLS with a certificate issued
	// for the public domain, which does not validate against 127.0.0.1. Bound
	// to loopback specifically, so this never becomes an unencrypted way in.
	go func() {
		probe := &http.Server{
			Addr:         "127.0.0.1:" + cfg.Port,
			Handler:      srv.Handler,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		if err := probe.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("the loopback probe listener stopped")
		}
	}()

	logger.Info().
		Str("domain", cfg.Domain).
		Str("cache", cfg.TLSCacheDir).
		Msg("serving HTTPS with an automatic certificate")

	// Empty strings: the certificate comes from the autocert manager in
	// TLSConfig, not from files on disk.
	return srv.ListenAndServeTLS("", "")
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

```bash
go test ./cmd/teamup/ -run 'ServeMode|BaseURL' -v
```
Expected: five PASS.

- [ ] **Step 6: Wire it into `runServer`**

In `cmd/teamup/main.go`, immediately after `env.Parse` succeeds (after line 318), add:

```go
	cfg.BaseURL = resolveBaseURL(cfg)
```

Then replace lines 621-624:

```go
	logger.Info().Str("port", cfg.Port).Msg("Server starting")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
```

with:

```go
	if err := listenAndServe(srv, cfg, logger); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
```

`srv.Addr` is set by `listenAndServe`, so the `Addr:` line in the `http.Server` literal at line 572-573 is now redundant — delete it to avoid two places claiming to decide the port.

- [ ] **Step 7: Document the new variables**

In `.env.example`, under `# Server`:

```bash
# Set this to a public hostname to serve HTTPS with a certificate TeamUp
# obtains and renews by itself. Leave it empty for local development, for
# ./start.sh --lan, or when something in front already terminates TLS.
# Requires ports 80 and 443 to be reachable from the internet.
TEAMUP_DOMAIN=

# Where issued certificates are kept. This must survive a restart: without a
# persistent directory every restart requests a new certificate and a handful
# of restarts is enough to hit Let's Encrypt's rate limit.
TEAMUP_TLS_CACHE=/var/lib/teamup/certs
```

- [ ] **Step 8: Verify nothing regressed for local development**

```bash
go build ./... && go test ./cmd/teamup/ -v
```
Expected: PASS.

```bash
./start.sh
```
Expected: still starts on `http://localhost:8080`, and the log line reads `serving plain HTTP`. Sign in to confirm the derived base URL did not break the session cookie. Stop with Ctrl-C, then `./stop.sh`.

- [ ] **Step 9: Commit**

```bash
git add cmd/teamup/serve.go cmd/teamup/serve_test.go cmd/teamup/main.go .env.example
git commit -m "feat: get a certificate without being asked twice

Setting TEAMUP_DOMAIN now serves HTTPS with a certificate TeamUp obtains
and renews itself, so no reverse proxy is needed in front — the server
already proxies LiveKit signalling. TEAMUP_BASE_URL is derived from the
domain rather than defaulting to localhost, which was silently wrong on
every real deployment and only showed up at sign-in."
```

---

## Task 5: Refuse to start with a placeholder secret

`values.yaml` shipped `changeme` for four services and `.env.example` ships `your-super-secret-key-change-in-production` for `TEAMUP_AUTH_SECRET`. A copied-and-run `.env` in production makes every session token forgeable, and nothing complains.

**Files:**
- Modify: `cmd/teamup/serve.go`, `cmd/teamup/serve_test.go`, `cmd/teamup/main.go` (`runServer`)

**Interfaces:**
- Consumes: `Config` from Task 4.
- Produces: `validateProduction(Config) error`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/teamup/serve_test.go`:

```go
func TestDevelopmentToleratesAnEmptySecret(t *testing.T) {
	if err := validateProduction(Config{Environment: "development"}); err != nil {
		t.Fatalf("development was blocked: %v", err)
	}
}

func TestProductionRejectsAnEmptySecret(t *testing.T) {
	if err := validateProduction(Config{Environment: "production"}); err == nil {
		t.Fatal("production started with no auth secret")
	}
}

func TestProductionRejectsTheExampleSecret(t *testing.T) {
	cfg := Config{Environment: "production", AuthSecret: "your-super-secret-key-change-in-production"}

	if err := validateProduction(cfg); err == nil {
		t.Fatal("production started with the secret straight out of .env.example")
	}
}

func TestProductionAcceptsARealSecret(t *testing.T) {
	cfg := Config{Environment: "production", AuthSecret: "S1mVYcJm0kQ4dQ0m3Yl2vQ1p8zVX9wKq"}

	if err := validateProduction(cfg); err != nil {
		t.Fatalf("a real secret was rejected: %v", err)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

```bash
go test ./cmd/teamup/ -run Production -v
```
Expected: FAIL — `undefined: validateProduction`.

- [ ] **Step 3: Implement it**

Append to `cmd/teamup/serve.go` (and add `"fmt"` and `"strings"` to its imports):

```go
// exampleAuthSecret is the value in .env.example. Somebody who copied that file
// and deployed it has a signing key printed in the repository.
const exampleAuthSecret = "your-super-secret-key-change-in-production"

// validateProduction refuses to start with credentials that were never changed.
//
// An empty or published signing key does not fail loudly — tokens still verify,
// against a key anybody can read here — so nothing would ever surface it. The
// check is confined to production so local development stays frictionless.
func validateProduction(cfg Config) error {
	if cfg.Environment != "production" {
		return nil
	}

	switch strings.TrimSpace(cfg.AuthSecret) {
	case "":
		return fmt.Errorf("TEAMUP_AUTH_SECRET must be set when TEAMUP_ENV=production: without it every session token is forgeable")
	case exampleAuthSecret:
		return fmt.Errorf("TEAMUP_AUTH_SECRET is still the example value from .env.example: generate one with `openssl rand -base64 32`")
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

```bash
go test ./cmd/teamup/ -run Production -v
```
Expected: four PASS.

- [ ] **Step 5: Call it**

In `cmd/teamup/main.go`, in `runServer`, immediately after the `cfg.BaseURL = resolveBaseURL(cfg)` line added in Task 4:

```go
	if err := validateProduction(cfg); err != nil {
		return err
	}
```

- [ ] **Step 6: Verify by hand**

```bash
TEAMUP_ENV=production TEAMUP_AUTH_SECRET= \
TEAMUP_DATABASE_URL=postgres://teamup:teamup@localhost:5432/teamup?sslmode=disable \
TEAMUP_REDIS_URL=redis://localhost:6379 \
TEAMUP_MINIO_ENDPOINT=localhost:9000 TEAMUP_MINIO_ACCESS_KEY=x TEAMUP_MINIO_SECRET_KEY=x TEAMUP_MINIO_BUCKET=x \
TEAMUP_LIVEKIT_URL=http://localhost:7880 TEAMUP_LIVEKIT_API_KEY=x TEAMUP_LIVEKIT_API_SECRET=x \
go run ./cmd/teamup server
```
Expected: exits with the `TEAMUP_AUTH_SECRET must be set` message, without connecting to anything.

- [ ] **Step 7: Commit**

```bash
git add cmd/teamup/serve.go cmd/teamup/serve_test.go cmd/teamup/main.go
git commit -m "fix: refuse to run in production with the example signing key

.env.example ships a placeholder TEAMUP_AUTH_SECRET, and a deployment
that copied it had a signing key published in this repository. Nothing
surfaced it, because tokens signed with a guessable key still verify."
```

---

## Task 6: A production Compose stack that actually starts

`docker-compose.prod.yml` publishes PostgreSQL, Redis and MinIO to the host, references an image nobody builds, never runs migrations, healthchecks the app with a binary that is not in the image, and needs an nginx whose config hardcodes `teamup.example.com`.

**Files:**
- Modify: `docker-compose.prod.yml` (full rewrite), `Makefile`
- Delete: `nginx.conf`, `helm/`

**Interfaces:**
- Consumes: the image from Task 1, `teamup healthcheck` from Task 2, `TEAMUP_DOMAIN`/`TEAMUP_TLS_CACHE` from Task 4.
- Produces: a `compose.yml` published as a release asset in Task 9.

- [ ] **Step 1: Rewrite `docker-compose.prod.yml`**

```yaml
# Production stack. There is no reverse proxy: TeamUp terminates TLS itself
# when TEAMUP_DOMAIN is set, and already proxies LiveKit signalling in-process.
#
# Only the app's ports and LiveKit's media range are published. PostgreSQL,
# Redis and MinIO are reachable only from the compose network — publishing them,
# as this file used to, exposed the database to the internet on any public VM.

services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: teamup
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: teamup
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U teamup -d teamup"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  redis:
    image: redis:7-alpine
    command: redis-server --requirepass ${REDIS_PASSWORD:?set REDIS_PASSWORD in .env}
    volumes:
      - redis-data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "${REDIS_PASSWORD}", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: ${MINIO_ACCESS_KEY:?set MINIO_ACCESS_KEY in .env}
      MINIO_ROOT_PASSWORD: ${MINIO_SECRET_KEY:?set MINIO_SECRET_KEY in .env}
    volumes:
      - minio-data:/data
    healthcheck:
      test: ["CMD", "mc", "ready", "local"]
      interval: 30s
      timeout: 10s
      retries: 3
    restart: unless-stopped

  livekit:
    image: livekit/livekit-server:latest
    environment:
      LIVEKIT_CONFIG: |
        port: 7880
        rtc:
          tcp_port: 7881
          port_range_start: 50000
          port_range_end: 60000
          use_external_ip: true
        keys:
          "${LIVEKIT_API_KEY}": "${LIVEKIT_API_SECRET}"
        redis:
          address: "redis:6379"
          password: "${REDIS_PASSWORD}"
    # Media is UDP straight to this container and cannot go through TeamUp's
    # signalling proxy, so this range has to be reachable from the internet.
    ports:
      - "7881:7881"
      - "50000-60000:50000-60000/udp"
    depends_on:
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:7880/"]
      interval: 10s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  # Migrations run as a one-shot before the app starts. Nothing used to run
  # them in production at all: the entrypoint went straight to `server`, so a
  # fresh deployment came up against an empty database.
  teamup-migrate:
    image: ghcr.io/${GITHUB_REPOSITORY:-himanshu-kr-jha/teamup.sh}:${TEAMUP_VERSION:-latest}
    command: ["migrate", "up"]
    environment:
      TEAMUP_DATABASE_URL: postgres://teamup:${POSTGRES_PASSWORD}@postgres:5432/teamup?sslmode=disable
      TEAMUP_REDIS_URL: redis://:${REDIS_PASSWORD}@redis:6379
      TEAMUP_MINIO_ENDPOINT: minio:9000
      TEAMUP_MINIO_ACCESS_KEY: ${MINIO_ACCESS_KEY}
      TEAMUP_MINIO_SECRET_KEY: ${MINIO_SECRET_KEY}
      TEAMUP_MINIO_BUCKET: teamup-files
      TEAMUP_LIVEKIT_URL: http://livekit:7880
      TEAMUP_LIVEKIT_API_KEY: ${LIVEKIT_API_KEY}
      TEAMUP_LIVEKIT_API_SECRET: ${LIVEKIT_API_SECRET}
    depends_on:
      postgres:
        condition: service_healthy
    restart: "no"

  teamup:
    image: ghcr.io/${GITHUB_REPOSITORY:-himanshu-kr-jha/teamup.sh}:${TEAMUP_VERSION:-latest}
    ports:
      - "80:80"
      - "443:443"
    environment:
      # No colon before ? on purpose: this errors when TEAMUP_DOMAIN is missing
      # from .env entirely, but allows it to be explicitly empty, which is how
      # the smoke test runs the stack on plain HTTP with no certificate.
      TEAMUP_DOMAIN: ${TEAMUP_DOMAIN?set TEAMUP_DOMAIN in .env}
      TEAMUP_TLS_CACHE: /var/lib/teamup/certs
      TEAMUP_DATABASE_URL: postgres://teamup:${POSTGRES_PASSWORD}@postgres:5432/teamup?sslmode=disable
      TEAMUP_REDIS_URL: redis://:${REDIS_PASSWORD}@redis:6379
      TEAMUP_MINIO_ENDPOINT: minio:9000
      TEAMUP_MINIO_ACCESS_KEY: ${MINIO_ACCESS_KEY}
      TEAMUP_MINIO_SECRET_KEY: ${MINIO_SECRET_KEY}
      TEAMUP_MINIO_BUCKET: teamup-files
      TEAMUP_MINIO_SECURE: "false"
      TEAMUP_LIVEKIT_URL: http://livekit:7880
      TEAMUP_LIVEKIT_API_KEY: ${LIVEKIT_API_KEY}
      TEAMUP_LIVEKIT_API_SECRET: ${LIVEKIT_API_SECRET}
      TEAMUP_AUTH_SECRET: ${AUTH_SECRET:?set AUTH_SECRET in .env}
      TEAMUP_VAPID_PUBLIC_KEY: ${VAPID_PUBLIC_KEY}
      TEAMUP_VAPID_PRIVATE_KEY: ${VAPID_PRIVATE_KEY}
      TEAMUP_ENV: production
      TEAMUP_LOG_LEVEL: info
    volumes:
      # Certificates must outlive the container: without this, every restart
      # requests a new one and a few restarts hit Let's Encrypt's rate limit.
      - teamup-certs:/var/lib/teamup/certs
    depends_on:
      teamup-migrate:
        condition: service_completed_successfully
      redis:
        condition: service_healthy
      minio:
        condition: service_healthy
      livekit:
        condition: service_healthy
    # The binary checks itself: this image is distroless and has no shell and
    # no wget, so a shell-based healthcheck could never pass.
    healthcheck:
      test: ["/teamup", "healthcheck"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s
    restart: unless-stopped

volumes:
  postgres-data:
  redis-data:
  minio-data:
  teamup-certs:
```

`TEAMUP_PORT` is not set here, so it keeps its `envDefault` of `8080`. That is the port the loopback probe listener added in Task 4 binds to, and the port `teamup healthcheck` dials — it is never published to the host, so it is reachable only from inside the container.

- [ ] **Step 2: Delete nginx and the empty chart**

```bash
git rm nginx.conf
git rm -r helm/
```

`helm/teamup/templates/` is empty, so `helm install` reports success and creates no Deployment, Service or Ingress — worse than shipping no chart at all.

- [ ] **Step 3: Remove the `helm` target from the Makefile**

Delete these lines:

```make
# Helm
helm:
	@echo "Packaging Helm chart..."
	helm package helm/teamup -d $(BUILD_DIR)/helm
```

and remove `helm` from the `.PHONY` line at the top and the `helm` entry from the `help` target's output.

- [ ] **Step 4: Verify the Compose file parses and the guards fire**

```bash
docker compose -f docker-compose.prod.yml config >/dev/null
```
Expected: fails, naming the first missing variable (`POSTGRES_PASSWORD`) — the `:?` guards working.

```bash
POSTGRES_PASSWORD=x REDIS_PASSWORD=x MINIO_ACCESS_KEY=x MINIO_SECRET_KEY=x \
LIVEKIT_API_KEY=x LIVEKIT_API_SECRET=x AUTH_SECRET=x TEAMUP_DOMAIN=teamup.example.com \
docker compose -f docker-compose.prod.yml config >/dev/null && echo OK
```
Expected: `OK`, and no `version` warning.

```bash
POSTGRES_PASSWORD=x REDIS_PASSWORD=x MINIO_ACCESS_KEY=x MINIO_SECRET_KEY=x \
LIVEKIT_API_KEY=x LIVEKIT_API_SECRET=x AUTH_SECRET=x TEAMUP_DOMAIN=teamup.example.com \
docker compose -f docker-compose.prod.yml config | grep -E "^\s+- \"(5432|6379|9000|9001):"
```
Expected: no output — the database, cache and object store are no longer published.

- [ ] **Step 5: Commit**

```bash
git add docker-compose.prod.yml Makefile cmd/teamup/serve.go cmd/teamup/serve_test.go
git commit -m "fix: make the production stack start, and stop publishing the database

The stack referenced an image nobody built, never ran migrations, and
healthchecked the app with wget against a distroless image. It also
published PostgreSQL, Redis and MinIO to the host, which on a public VM
is the whole database exposed. Migrations now run as a one-shot before
the app, and only the app and LiveKit's media range are published.

Deletes nginx.conf, now that TeamUp terminates TLS itself, and the Helm
chart, whose templates directory was empty — helm install reported
success and deployed nothing."
```

---

## Task 7: Generate secrets instead of shipping them

**Files:**
- Create: `scripts/gen-env.sh`
- Modify: `Makefile`

**Interfaces:**
- Consumes: the variable names used by `docker-compose.prod.yml` in Task 6.
- Produces: `scripts/gen-env.sh`, published as a release asset in Task 9.

- [ ] **Step 1: Write `scripts/gen-env.sh`**

```bash
#!/bin/bash
# Generates .env with fresh secrets for a production install.
#
# Usage: gen-env.sh <domain>
#
# Every value here used to be a placeholder committed to the repository —
# `changeme`, `minioadmin`, and an auth secret printed in .env.example. A
# deployment that copied those had a signing key anybody could read.
set -euo pipefail

DOMAIN="${1:-}"
if [ -z "$DOMAIN" ]; then
  echo "usage: $0 <domain>    e.g. $0 teamup.example.com" >&2
  exit 1
fi

if [ -e .env ]; then
  echo "❌ .env already exists. Move it aside first — overwriting it would" >&2
  echo "   lock you out of your own database, whose password is in there." >&2
  exit 1
fi

if ! command -v openssl >/dev/null 2>&1; then
  echo "❌ openssl is needed to generate secrets." >&2
  exit 1
fi

# base64 with the URL-unsafe characters removed: these end up in connection
# strings and LiveKit's YAML, where a stray / or + needs escaping.
secret() { openssl rand -base64 32 | tr -d '=+/' | cut -c1-32; }

cat > .env <<EOF
# Generated by scripts/gen-env.sh — keep this file secret and back it up.
TEAMUP_DOMAIN=$DOMAIN

POSTGRES_PASSWORD=$(secret)
REDIS_PASSWORD=$(secret)
MINIO_ACCESS_KEY=$(secret)
MINIO_SECRET_KEY=$(secret)
LIVEKIT_API_KEY=$(secret)
LIVEKIT_API_SECRET=$(secret)
AUTH_SECRET=$(secret)

# Web Push. Leave empty to run without desktop notifications; generate a real
# VAPID pair later with: teamup wizard
VAPID_PUBLIC_KEY=
VAPID_PRIVATE_KEY=

# Pin a release instead of tracking latest, e.g. TEAMUP_VERSION=v1.0.0
TEAMUP_VERSION=latest
EOF

chmod 600 .env

echo "✅ Wrote .env for $DOMAIN"
echo ""
echo "   Before starting, make sure:"
echo "   • $DOMAIN has an A record pointing at this machine"
echo "   • ports 80 and 443 are open (the certificate is fetched over them)"
echo "   • UDP 50000-60000 and TCP 7881 are open, or calls will not connect"
echo ""
echo "   Then: docker compose -f docker-compose.prod.yml up -d"
```

- [ ] **Step 2: Make it executable and add the Makefile target**

```bash
chmod +x scripts/gen-env.sh
```

Add to the `Makefile` (and add `secrets` to `.PHONY` and to the `help` output):

```make
# Generate .env with fresh secrets. DOMAIN is required.
secrets:
	@test -n "$(DOMAIN)" || { echo "usage: make secrets DOMAIN=teamup.example.com" >&2; exit 1; }
	@./scripts/gen-env.sh "$(DOMAIN)"
```

- [ ] **Step 3: Verify it refuses to clobber and produces a valid file**

```bash
cd "$(mktemp -d)" && cp -r "$OLDPWD/scripts" . && touch .env
./scripts/gen-env.sh teamup.example.com; echo "exit=$?"
```
Expected: the "already exists" message and `exit=1`.

```bash
rm .env && ./scripts/gen-env.sh teamup.example.com && cat .env && stat -c '%a' .env
```
Expected: seven distinct 32-character secrets, `TEAMUP_DOMAIN=teamup.example.com`, and permissions `600`.

```bash
./scripts/gen-env.sh; echo "exit=$?"
```
Expected: the usage message and `exit=1`.

Return with `cd -` and delete the temporary directory.

- [ ] **Step 4: Commit**

```bash
git add scripts/gen-env.sh Makefile
git commit -m "feat: generate secrets rather than shipping placeholders

Every credential had a committed default — changeme, minioadmin, and an
auth secret printed in .env.example. gen-env.sh writes fresh ones, mode
600, and refuses to overwrite an existing .env."
```

---

## Task 8: The systemd install path

Per the spec's dependency decision, this path installs four services and is **not** lighter than Docker. It exists for operators who will not run containers.

**Files:**
- Create: `scripts/install-systemd.sh`, `scripts/install-services.sh`

**Interfaces:**
- Consumes: `TEAMUP_TLS_CACHE` from Task 4, the release binary from Task 9.
- Produces: nothing other tasks consume.

- [ ] **Step 1: Write `scripts/install-systemd.sh`**

```bash
#!/bin/bash
# Installs the teamup binary as a systemd service.
#
# Usage: sudo ./install-systemd.sh <path-to-binary> <domain>
set -euo pipefail

BINARY="${1:-}"
DOMAIN="${2:-}"

if [ -z "$BINARY" ] || [ -z "$DOMAIN" ]; then
  echo "usage: sudo $0 <path-to-teamup-binary> <domain>" >&2
  exit 1
fi
if [ "$(id -u)" -ne 0 ]; then
  echo "❌ Run this with sudo: it creates a system user and writes to /etc." >&2
  exit 1
fi
if [ ! -x "$BINARY" ]; then
  echo "❌ $BINARY is not an executable file." >&2
  exit 1
fi

install -m 755 "$BINARY" /usr/local/bin/teamup

if ! id teamup >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin teamup
fi

mkdir -p /etc/teamup
if [ ! -e /etc/teamup/teamup.env ]; then
  secret() { openssl rand -base64 32 | tr -d '=+/' | cut -c1-32; }
  cat > /etc/teamup/teamup.env <<EOF
TEAMUP_ENV=production
TEAMUP_LOG_LEVEL=info
TEAMUP_DOMAIN=$DOMAIN
TEAMUP_TLS_CACHE=/var/lib/teamup/certs
TEAMUP_AUTH_SECRET=$(secret)

# Filled in by install-services.sh, or point these at services you already run.
TEAMUP_DATABASE_URL=
TEAMUP_REDIS_URL=
TEAMUP_MINIO_ENDPOINT=
TEAMUP_MINIO_ACCESS_KEY=
TEAMUP_MINIO_SECRET_KEY=
TEAMUP_MINIO_BUCKET=teamup-files
TEAMUP_LIVEKIT_URL=
TEAMUP_LIVEKIT_API_KEY=
TEAMUP_LIVEKIT_API_SECRET=
EOF
  chmod 600 /etc/teamup/teamup.env
  chown teamup:teamup /etc/teamup/teamup.env
  echo "✅ Wrote /etc/teamup/teamup.env with a generated auth secret"
else
  echo "ℹ️  /etc/teamup/teamup.env already exists; leaving it alone"
fi

cat > /etc/systemd/system/teamup.service <<'EOF'
[Unit]
Description=TeamUp collaboration platform
After=network-online.target postgresql.service redis-server.service
Wants=network-online.target

[Service]
Type=exec
User=teamup
EnvironmentFile=/etc/teamup/teamup.env
# Schema first, every time, so an upgrade cannot serve against an old database.
ExecStartPre=/usr/local/bin/teamup migrate up
ExecStart=/usr/local/bin/teamup server
# Lets a non-root process bind 443 for TLS.
AmbientCapabilities=CAP_NET_BIND_SERVICE
# Creates /var/lib/teamup owned by this user — where the certificate cache
# lives, which has to survive restarts or Let's Encrypt starts refusing.
StateDirectory=teamup
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload

echo ""
echo "✅ Installed. Fill in the service URLs in /etc/teamup/teamup.env"
echo "   (or run ./install-services.sh first), then:"
echo ""
echo "     sudo systemctl enable --now teamup"
echo "     sudo systemctl status teamup"
echo "     sudo journalctl -u teamup -f"
```

- [ ] **Step 2: Write `scripts/install-services.sh`**

```bash
#!/bin/bash
# Installs the four services TeamUp needs, on Debian and Ubuntu.
#
# TeamUp requires PostgreSQL, Redis, an S3-compatible store and LiveKit. This
# path does not make that shorter — it only avoids containers. If you are
# willing to run Docker, docker-compose.prod.yml is less work and less to keep
# patched.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "❌ Run this with sudo." >&2
  exit 1
fi
if ! command -v apt-get >/dev/null 2>&1; then
  echo "❌ This script supports Debian and Ubuntu only." >&2
  echo "   On other distributions, install PostgreSQL 16, Redis 7, MinIO and" >&2
  echo "   LiveKit yourself, then fill in /etc/teamup/teamup.env." >&2
  exit 1
fi

ENV_FILE=/etc/teamup/teamup.env
if [ ! -e "$ENV_FILE" ]; then
  echo "❌ $ENV_FILE not found. Run install-systemd.sh first." >&2
  exit 1
fi

secret() { openssl rand -base64 32 | tr -d '=+/' | cut -c1-32; }

# Replaces KEY=... in place, so re-running does not append duplicates that
# would silently shadow each other.
set_env() {
  local key="$1" value="$2"
  if grep -q "^${key}=" "$ENV_FILE"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
  else
    echo "${key}=${value}" >> "$ENV_FILE"
  fi
}

echo "📦 Installing PostgreSQL and Redis..."
apt-get update -qq
apt-get install -y -qq postgresql postgresql-contrib redis-server curl

DB_PASSWORD="$(secret)"
sudo -u postgres psql -v ON_ERROR_STOP=1 <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'teamup') THEN
    CREATE ROLE teamup LOGIN PASSWORD '${DB_PASSWORD}';
  ELSE
    ALTER ROLE teamup PASSWORD '${DB_PASSWORD}';
  END IF;
END \$\$;
SQL
sudo -u postgres psql -tc "SELECT 1 FROM pg_database WHERE datname='teamup'" \
  | grep -q 1 || sudo -u postgres createdb -O teamup teamup

# The schema needs these, and only a superuser may create them, so it happens
# here rather than in the migration run by the teamup user.
for ext in uuid-ossp pgcrypto pg_trgm btree_gin; do
  sudo -u postgres psql -d teamup -c "CREATE EXTENSION IF NOT EXISTS \"$ext\";"
done

REDIS_PASSWORD="$(secret)"
sed -i "s/^# *requirepass .*/requirepass ${REDIS_PASSWORD}/" /etc/redis/redis.conf
grep -q "^requirepass " /etc/redis/redis.conf || echo "requirepass ${REDIS_PASSWORD}" >> /etc/redis/redis.conf
systemctl restart redis-server

echo "📦 Installing MinIO..."
ARCH="$(dpkg --print-architecture)"
curl -fsSL "https://dl.min.io/server/minio/release/linux-${ARCH}/minio" -o /usr/local/bin/minio
chmod +x /usr/local/bin/minio
id minio-user >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin minio-user
mkdir -p /var/lib/minio && chown minio-user:minio-user /var/lib/minio

MINIO_ACCESS_KEY="$(secret)"
MINIO_SECRET_KEY="$(secret)"
cat > /etc/default/minio <<EOF
MINIO_ROOT_USER=${MINIO_ACCESS_KEY}
MINIO_ROOT_PASSWORD=${MINIO_SECRET_KEY}
EOF
chmod 600 /etc/default/minio

cat > /etc/systemd/system/minio.service <<'EOF'
[Unit]
Description=MinIO object storage
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=minio-user
EnvironmentFile=/etc/default/minio
# Bound to loopback: TeamUp reaches it locally, and nothing else should.
ExecStart=/usr/local/bin/minio server /var/lib/minio --address 127.0.0.1:9000
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

echo "📦 Installing LiveKit..."
curl -fsSL https://get.livekit.io | bash
id livekit >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin livekit

LIVEKIT_API_KEY="$(secret)"
LIVEKIT_API_SECRET="$(secret)"
mkdir -p /etc/livekit
cat > /etc/livekit/config.yaml <<EOF
port: 7880
rtc:
  tcp_port: 7881
  port_range_start: 50000
  port_range_end: 60000
  use_external_ip: true
keys:
  "${LIVEKIT_API_KEY}": "${LIVEKIT_API_SECRET}"
redis:
  address: "127.0.0.1:6379"
  password: "${REDIS_PASSWORD}"
EOF
chmod 640 /etc/livekit/config.yaml
chown root:livekit /etc/livekit/config.yaml

cat > /etc/systemd/system/livekit.service <<'EOF'
[Unit]
Description=LiveKit server
After=network-online.target redis-server.service
Wants=network-online.target

[Service]
Type=exec
User=livekit
ExecStart=/usr/local/bin/livekit-server --config /etc/livekit/config.yaml
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

set_env TEAMUP_DATABASE_URL "postgres://teamup:${DB_PASSWORD}@127.0.0.1:5432/teamup?sslmode=disable"
set_env TEAMUP_REDIS_URL "redis://:${REDIS_PASSWORD}@127.0.0.1:6379"
set_env TEAMUP_MINIO_ENDPOINT "127.0.0.1:9000"
set_env TEAMUP_MINIO_ACCESS_KEY "${MINIO_ACCESS_KEY}"
set_env TEAMUP_MINIO_SECRET_KEY "${MINIO_SECRET_KEY}"
set_env TEAMUP_MINIO_BUCKET "teamup-files"
set_env TEAMUP_LIVEKIT_URL "http://127.0.0.1:7880"
set_env TEAMUP_LIVEKIT_API_KEY "${LIVEKIT_API_KEY}"
set_env TEAMUP_LIVEKIT_API_SECRET "${LIVEKIT_API_SECRET}"

systemctl daemon-reload
systemctl enable --now minio livekit

echo ""
echo "✅ PostgreSQL, Redis, MinIO and LiveKit are installed and running."
echo "   Credentials are written to $ENV_FILE."
echo ""
echo "   Open UDP 50000-60000 and TCP 7881 for calls, and 80/443 for the app."
echo "   Then: sudo systemctl enable --now teamup"
```

- [ ] **Step 3: Check both scripts for shell errors**

```bash
chmod +x scripts/install-systemd.sh scripts/install-services.sh
bash -n scripts/install-systemd.sh && bash -n scripts/install-services.sh && echo "syntax OK"
```
Expected: `syntax OK`.

```bash
command -v shellcheck >/dev/null && shellcheck scripts/*.sh || echo "shellcheck not installed, skipping"
```

- [ ] **Step 4: Verify the guard paths without root**

```bash
./scripts/install-systemd.sh; echo "exit=$?"
./scripts/install-systemd.sh /bin/true example.com; echo "exit=$?"
```
Expected: usage message with `exit=1`, then the "Run this with sudo" message with `exit=1`.

> A full end-to-end run needs a throwaway Debian or Ubuntu VM and root. Do that once by hand before the first release; it is not part of CI, since CI would have to become a privileged VM job to run it.

- [ ] **Step 5: Commit**

```bash
git add scripts/install-systemd.sh scripts/install-services.sh
git commit -m "feat: install TeamUp under systemd, without Docker

For operators who will not run containers. install-systemd.sh sets up
the user, env file and unit — with CAP_NET_BIND_SERVICE so a non-root
process can bind 443, and StateDirectory for the certificate cache.
install-services.sh installs the four required services on Debian and
Ubuntu; it does not make this path lighter than Docker, only
container-free."
```

---

## Task 9: CI and releases

There is no `.github/` at all, and every compose file and the chart referenced `ghcr.io/yourorg/teamup:latest` — an image nothing built.

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/release.yml`

**Interfaces:**
- Consumes: the Dockerfile from Task 1, `scripts/gen-env.sh` from Task 7, `docker-compose.prod.yml` from Task 6.
- Produces: `ghcr.io/himanshu-kr-jha/teamup.sh:{version,latest}` and release assets.

> Targets GitHub Actions and GHCR at `himanshu-kr-jha/teamup.sh`. This was an unconfirmed assumption when the plan was written, because no git remote existed then; it has since been confirmed and the image path is settled.

- [ ] **Step 1: Write `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  go:
    name: Go
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16-alpine
        env:
          POSTGRES_USER: teamup
          POSTGRES_PASSWORD: teamup
          POSTGRES_DB: postgres
        ports: ["5432:5432"]
        options: >-
          --health-cmd "pg_isready -U teamup"
          --health-interval 10s --health-timeout 5s --health-retries 5
    env:
      TEST_DATABASE_URL: postgres://teamup:teamup@localhost:5432/teamup_test?sslmode=disable
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: corepack enable

      - name: Build
        run: make build

      # Creates and migrates the template database each package clones from.
      - name: Prepare the test database
        run: make test-db

      - name: Test
        run: make test-unit

      - name: Lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  web:
    name: Web
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: corepack enable
      - working-directory: web
        run: pnpm install --frozen-lockfile
      - working-directory: web
        run: pnpm lint
      # `build` is `tsc && vite build`, so this also type-checks.
      - working-directory: web
        run: pnpm run build

  image:
    name: Image builds from a clean checkout
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      # This job is the regression test for the embed bug: the build used to
      # succeed only on a machine that had already run `make build` locally.
      # A CI checkout has no cmd/teamup/web/dist, so nothing can hide it.
      - uses: docker/build-push-action@v6
        with:
          context: .
          push: false
          tags: teamup:ci
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

- [ ] **Step 2: Write `.github/workflows/release.yml`**

```yaml
name: Release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write
  packages: write

jobs:
  image:
    name: Publish image
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/${{ github.repository }}
          tags: |
            type=semver,pattern={{version}}
            type=raw,value=latest

      - uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ github.ref_name }}
            COMMIT=${{ github.sha }}
          cache-from: type=gha

  binaries:
    name: Publish binaries
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: corepack enable

      # Builds the web app once; build-all then compiles it into each binary.
      - run: make build-all
        env:
          VERSION: ${{ github.ref_name }}

      - name: Attach artifacts
        uses: softprops/action-gh-release@v2
        with:
          files: |
            bin/teamup-linux-amd64
            bin/teamup-linux-arm64
            docker-compose.prod.yml
            scripts/gen-env.sh
            scripts/install-systemd.sh
            scripts/install-services.sh
          generate_release_notes: true
```

- [ ] **Step 3: Verify the workflow files parse**

```bash
python3 -c "import yaml,sys; [yaml.safe_load(open(f)) for f in sys.argv[1:]]; print('YAML OK')" \
  .github/workflows/ci.yml .github/workflows/release.yml
```
Expected: `YAML OK`.

- [ ] **Step 4: Confirm the referenced Make targets exist**

```bash
grep -E '^(build|build-all|test-db|test-unit):' Makefile
```
Expected: all four present. `make build-all` must not have been removed by Task 6's Makefile edit.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml .github/workflows/release.yml
git commit -m "ci: build, test and publish the image nothing was building

Every compose file and the chart referenced ghcr.io/yourorg/teamup and
no workflow existed to produce it. Adds lint, test and web checks, an
image build from a clean checkout — the regression test for the embed
bug — and a tag-triggered multi-arch publish with release binaries."
```

---

## Task 10: A smoke test that proves the stack works

Most of the defects this plan fixes existed because the deployment was never once executed in the shape it claimed to support. This job executes it.

**Files:**
- Create: `.github/workflows/smoke.yml`

**Interfaces:**
- Consumes: `docker-compose.prod.yml` (Task 6), the image build (Task 1), `TEAMUP_AUTO_SETUP` (already in `Config`).
- Produces: nothing other tasks consume.

- [ ] **Step 1: Write `.github/workflows/smoke.yml`**

`TEAMUP_DOMAIN` is deliberately unset so no certificate is requested — CI has no public hostname, and ACME would fail. That exercises the plain-HTTP branch of `listenAndServe`.

```yaml
name: Smoke

on:
  push:
    branches: [main]
  pull_request:

jobs:
  compose:
    name: The stack comes up and serves
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3

      - name: Build the image under the tag compose expects
        uses: docker/build-push-action@v6
        with:
          context: .
          push: false
          load: true
          tags: ghcr.io/himanshu-kr-jha/teamup.sh:latest
          cache-from: type=gha

      # No TEAMUP_DOMAIN: CI has no public hostname, so the server stays on
      # plain HTTP and no certificate is requested.
      - name: Write a test .env
        run: |
          cat > .env <<'EOF'
          POSTGRES_PASSWORD=smoketest-postgres
          REDIS_PASSWORD=smoketest-redis
          MINIO_ACCESS_KEY=smoketest-minio-access
          MINIO_SECRET_KEY=smoketest-minio-secret
          LIVEKIT_API_KEY=smoketest-livekit-key
          LIVEKIT_API_SECRET=smoketest-livekit-secret
          AUTH_SECRET=smoketest-auth-secret-not-the-example-one
          TEAMUP_DOMAIN=
          TEAMUP_VERSION=latest
          EOF

      - name: Bring up the backing services
        run: docker compose -f docker-compose.prod.yml up -d postgres redis minio livekit

      - name: Run migrations
        run: docker compose -f docker-compose.prod.yml run --rm teamup-migrate

      - name: Start TeamUp
        run: |
          docker compose -f docker-compose.prod.yml run -d --name teamup-smoke \
            -p 8080:8080 \
            -e TEAMUP_PORT=8080 \
            -e TEAMUP_AUTO_SETUP=true \
            -e TEAMUP_SETUP_ORG_NAME=Smoke \
            -e TEAMUP_SETUP_ORG_SUBDOMAIN=smoke \
            -e TEAMUP_SETUP_ADMIN_NAME=Smoke \
            -e TEAMUP_SETUP_ADMIN_EMAIL=smoke@example.com \
            -e TEAMUP_SETUP_ADMIN_PASSWORD=smoke-test-password-123 \
            teamup server

      - name: Wait for readiness
        run: |
          for i in $(seq 1 60); do
            if curl -fsS http://localhost:8080/readyz >/dev/null 2>&1; then
              echo "ready after ${i}s"; exit 0
            fi
            sleep 1
          done
          echo "never became ready"; docker logs teamup-smoke; exit 1

      # The binary checking itself, inside the distroless container — the
      # failure mode that only appears when actually run.
      - name: The container's own healthcheck passes
        run: docker exec teamup-smoke /teamup healthcheck

      - name: The web app is served
        run: |
          curl -fsS http://localhost:8080/ | grep -qi '<div id="root"' \
            || { echo "the embedded web app was not served"; exit 1; }

      - name: Sign in
        run: |
          code=$(curl -s -o /tmp/login.json -w '%{http_code}' \
            -X POST http://localhost:8080/auth/login \
            -H 'Content-Type: application/json' \
            -d '{"email":"smoke@example.com","password":"smoke-test-password-123"}')
          echo "login returned $code"
          cat /tmp/login.json
          test "$code" = "200"

      - name: Show logs on failure
        if: failure()
        run: |
          docker logs teamup-smoke || true
          docker compose -f docker-compose.prod.yml logs || true
```

- [ ] **Step 2: Confirm the local-password login route exists**

The "Sign in" step assumes `POST /auth/login` takes an email and password. Verify before committing:

```bash
grep -rn --include="*.go" '"/auth/login\|auth/login' cmd/teamup/main.go internal/auth/
```

If the route differs (a different path, or a different JSON body), correct the step to match. If there is no local-password login route at all, replace that step with an unauthenticated assertion that still proves the API is live:

```yaml
      - name: The API is live
        run: |
          code=$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/api/wizard/status)
          test "$code" = "200"
```

- [ ] **Step 3: Verify the YAML parses**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/smoke.yml')); print('YAML OK')"
```
Expected: `YAML OK`.

- [ ] **Step 4: Run the equivalent locally before relying on CI**

```bash
docker build -t ghcr.io/himanshu-kr-jha/teamup.sh:latest .
cp .env .env.local.bak 2>/dev/null || true
# write the same test .env as the workflow, then:
docker compose -f docker-compose.prod.yml up -d postgres redis minio livekit
docker compose -f docker-compose.prod.yml run --rm teamup-migrate
```
Expected: migrations report `Migrations applied successfully`.

Restore `.env` afterwards and `docker compose -f docker-compose.prod.yml down -v`.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/smoke.yml
git commit -m "ci: actually run the production stack

Most of what was broken here was broken because the deployment was
never executed in the shape it claimed to support. This brings up the
real compose stack, runs migrations, waits for readiness, runs the
container's own healthcheck inside the distroless image, and signs in."
```

---

## Task 11: Documentation that matches what was built

**Files:**
- Modify: `docs/DEPLOYMENT.md`, `README.md`, `.env.example`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Restructure `docs/DEPLOYMENT.md`**

Read the current file first. **Keep** the opening "Why HTTPS decides what works" section and its table — it is what explains why the LAN path has no calls and no desktop notifications, and it is still true. **Delete** the manual certificate instructions, which autocert replaces.

Restructure the rest into four paths, in this order:

1. **On your own machine** — `cp .env.example .env && ./start.sh`.
2. **On your own network** — `./start.sh --lan`, with the existing explanation of what will not work and why.
3. **On a server, with Docker** — the three commands from the spec:
   ```bash
   curl -fsSLO https://github.com/himanshu-kr-jha/teamup.sh/releases/latest/download/docker-compose.prod.yml
   curl -fsSL  https://github.com/himanshu-kr-jha/teamup.sh/releases/latest/download/gen-env.sh | sh -s teamup.acme.com
   docker compose -f docker-compose.prod.yml up -d
   ```
   Prerequisites: a DNS A record for the domain pointing at the machine, and open ports 80, 443, TCP 7881 and UDP 50000–60000. State that ports 80 and 443 must be reachable *before* first start or the certificate cannot be issued.
4. **On a server, without Docker** — download the binary from the release, then `install-services.sh` and `install-systemd.sh`. State plainly that this installs four services and is not lighter than Docker.

Add a short **"Verifying HTTPS"** checklist, since CI cannot exercise the autocert branch:

```markdown
## Checking automatic HTTPS worked

CI cannot test this — it needs a real domain — so check it once by hand
after the first production start.

1. `docker compose -f docker-compose.prod.yml logs teamup | grep "automatic certificate"`
   should show the domain and cache directory.
2. `curl -I http://<domain>` should return `301` to the `https://` URL.
3. `curl -I https://<domain>` should return `200` with no certificate warning.
4. Restart (`docker compose -f docker-compose.prod.yml restart teamup`) and check
   the logs do **not** show a new certificate being requested — that would mean
   the cache volume is not persisting, and repeated restarts will hit Let's
   Encrypt's rate limit.
```

- [ ] **Step 2: Replace the interim note in `README.md`**

Delete the "**A note on the current state:**" paragraph in the Deploying section and replace it with:

```markdown
The short version, on a server with a domain pointed at it:

```bash
curl -fsSLO https://github.com/himanshu-kr-jha/teamup.sh/releases/latest/download/docker-compose.prod.yml
curl -fsSL  https://github.com/himanshu-kr-jha/teamup.sh/releases/latest/download/gen-env.sh | sh -s teamup.example.com
docker compose -f docker-compose.prod.yml up -d
```

TeamUp obtains its own TLS certificate on first start, so there is nothing to
configure and no reverse proxy to run. Ports 80 and 443 must be reachable
before you start it, and calls need TCP 7881 and UDP 50000–60000 open.

**One instance only.** WebSocket fan-out is in-process, so running more than one
replica means users connected to different instances stop seeing each other's
messages. Removing that limit is tracked as the Redis fan-out work.
```

Also update the Project Structure block: `helm/teamup/` is gone and `scripts/` is new.

- [ ] **Step 3: Check every command in the docs actually runs**

```bash
grep -oE '(make [a-z-]+)' README.md docs/DEPLOYMENT.md | sort -u
```
Cross-check each against `grep -E '^[a-z-]+:' Makefile`. Any target the docs mention that no longer exists — `make helm`, removed in Task 6 — is a broken instruction.

- [ ] **Step 4: Confirm no dead references remain**

```bash
grep -rn "nginx\|helm" README.md docs/DEPLOYMENT.md Makefile .env.example || echo "no stale references"
```
Expected: `no stale references`, or only prose that is still accurate.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/DEPLOYMENT.md .env.example
git commit -m "docs: describe the install that now exists

Replaces the manual certificate ceremony with the one-command install,
and the note saying the production path was being reworked. Adds a
by-hand checklist for automatic HTTPS, which CI cannot exercise because
it has no real domain."
```

---

## Final verification

Run all of this before declaring the work done.

- [ ] **Everything builds and tests pass**

```bash
make build && make test-unit && make lint
```

- [ ] **The image builds from committed content only**

```bash
rm -rf /tmp/teamup-final && mkdir -p /tmp/teamup-final
git archive HEAD | tar -x -C /tmp/teamup-final
docker build -t teamup:final /tmp/teamup-final
docker run --rm teamup:final version
```
Expected: build succeeds and `version` prints.

- [ ] **Local development is unchanged**

```bash
./start.sh
```
Expected: `serving plain HTTP` on `http://localhost:8080`, wizard reachable, sign-in works. Then `./stop.sh`.

- [ ] **Nothing still references what was deleted**

```bash
grep -rn "nginx.conf\|helm/teamup\|yourorg" \
  --include="*.md" --include="*.yml" --include="*.yaml" --include="Makefile" . \
  | grep -v docs/superpowers/ || echo "clean"
```
Expected: `clean`. Matches inside `docs/superpowers/` are the spec and this plan describing what was removed, which is correct.

- [ ] **The spec's out-of-scope items really are absent**

```bash
grep -rn --include="*.go" "PSubscribe\|redis.*Publish" internal/websocket/ || echo "no Redis fan-out, as intended"
```
Expected: `no Redis fan-out, as intended`.
