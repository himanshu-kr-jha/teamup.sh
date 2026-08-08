## Question

Confirm Go as the backend language for the single-binary, WebSocket-heavy, WebRTC-signaling service. Document the rationale, key libraries (standard library, `gorilla/websocket` or `nhooyr/websocket`, `pion/webrtc` for signaling if needed, LiveKit Go SDK), and project structure conventions.

Status: claimed

## Answer

**Decision: Go is confirmed as the backend language.**

### Rationale

1. **Single binary deployment** — Go compiles to a static binary with no runtime dependencies. This is the core requirement for "easily deployable inside the environment so a single product can be shipped to any startup." A single `teamup` binary + embedded frontend assets = zero-friction install.

2. **Concurrency model** — Goroutines + channels are purpose-built for WebSocket-heavy services. Handling 10K+ concurrent connections per instance is routine; Node/Python require worker clusters and external Redis for horizontal scaling.

3. **WebRTC ecosystem** —
   - `livekit/go-sdk` (official, maintained by LiveKit team) for SFU signaling, token generation, room management
   - `pion/webrtc` (pure Go WebRTC implementation) if any custom signaling or media handling is needed
   - No need for C++ bindings or external media servers — LiveKit handles the SFU, Go handles signaling

4. **WebSocket libraries** —
   - `nhooyr/websocket` (modern, actively maintained, supports compression, context-aware)
   - `gorilla/websocket` (older, more battle-tested, larger community)
   - Recommendation: `nhooyr/websocket` for new projects — better API, built-in compression (permessage-deflate), context cancellation

5. **Standard library richness** — `net/http`, `crypto/tls`, `database/sql`, `encoding/json`, `html/template`, `embed` (for embedding frontend/assets) — all production-grade without external deps.

6. **Observability built-in** — `expvar`, `net/http/pprof`, structured logging with `zerolog`/`zap`, Prometheus client (`prometheus/client_golang`) — minimal boilerplate.

7. **Cross-compilation** — `GOOS=linux GOARCH=amd64 go build` produces linux binary from macOS/Windows. Docker multi-stage builds are trivial (scratch/distroless base).

### Key Libraries

| Category | Library | Purpose |
|----------|---------|---------|
| WebSocket | `nhooyr/websocket` | Modern WebSocket with compression, context support |
| LiveKit | `github.com/livekit/server-sdk-go` | Token auth, room management, webhook handling |
| WebRTC (if needed) | `github.com/pion/webrtc/v3` | Pure Go WebRTC for custom signaling |
| Database | `github.com/jackc/pgx/v5` | Fast, feature-complete Postgres driver |
| ORM/Query | `github.com/stephenafamo/sqlc` or `ent` | Type-safe SQL (sqlc) or code-gen ORM (ent) |
| Config | `github.com/spf13/viper` or `github.com/caarlos0/env` | Env/config management |
| Logging | `github.com/rs/zerolog` | Zero-allocation JSON logging |
| Metrics | `github.com/prometheus/client_golang` | Prometheus exposition |
| CLI | `github.com/spf13/cobra` | CLI commands (migrate, server, wizard) |
| Migration | `github.com/golang-migrate/migrate/v4` | DB migrations |
| Auth | `golang.org/x/oauth2`, `github.com/coreos/go-oidc/v3` | OIDC/OAuth2 |

### Project Structure Convention

```
teamup/
├── cmd/
│   ├── teamup/           # Main entry point (server, migrate, wizard, version)
│   └── teamupctl/        # Admin CLI (user management, token debug)
├── internal/
│   ├── auth/             # OIDC, JWT, session, middleware
│   ├── chat/             # Chat hub, message persistence, search
│   ├── calls/            # LiveKit integration, signaling relay
│   ├── files/            # MinIO presigned URLs, upload handling
│   ├── notifications/    # Web Push, in-app notification center
│   ├── presence/         # User presence, status, WebSocket broadcast
│   ├── tasks/            # Jira-like task domain (CRUD, assignments, comments)
│   ├── org/              # Organizations, users, RBAC, teams
│   ├── websocket/        # Unified WS hub, connection registry, protocol
│   ├── wizard/           # Setup wizard handlers
│   └── observability/    # Logging, metrics, health, tracing
├── pkg/
│   ├── protocol/         # Shared WS message types (JSON schema)
│   └── errors/           # Error types, codes
├── web/                  # Embedded React frontend (built by Vite)
│   └── dist/             # Output of `npm run build` (gitignored)
├── migrations/           # SQL migration files (golang-migrate)
├── helm/                 # Helm chart
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

### Build & Deploy Artifacts

- **Single binary**: `CGO_ENABLED=0 go build -ldflags="-s -w" -o teamup ./cmd/teamup` (embeds `web/dist` via `//go:embed`)
- **Docker**: Multi-stage → `scratch` or `gcr.io/distroless/static` base (~15MB)
- **Helm**: `helm/` chart with values for image, config, MinIO, LiveKit, Postgres, ingress

### Open Questions (for later tickets)

- sqlc vs. ent for database layer — decide in ticket 11
- Exact WebSocket message protocol schema — ticket 03
- LiveKit webhook handling for recording/events — ticket 02

Status: resolved
