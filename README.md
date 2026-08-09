# TeamUp

**A self-hosted collaboration platform — chat, video calls, tasks and files, on your own server.**

TeamUp is what a team uses instead of Slack or Microsoft Teams when the
conversation should not live on somebody else's infrastructure. It is a single
Go binary with the web app compiled into it, backed by PostgreSQL, Redis, an
S3-compatible object store and LiveKit for calls. You run it, you own the data,
and there is no seat pricing.

It is open source under the MIT licence. Anyone may use it commercially, fork
it, or contribute — see [Contributing](#contributing).

---

## Features

- **Real-time chat** — channels, DMs, threads, reactions, typing indicators, read receipts, full-text and fuzzy search
- **Video calls** — LiveKit-powered multi-party video and audio, with screen sharing; call events appear in the channel they started from
- **Tasks** — a board with sprints, labels, comments and attachments
- **Files** — S3-compatible storage with presigned upload and download, including multipart for large files
- **Presence** — online, away and in-call status, plus availability and leave
- **Notifications** — Web Push (VAPID) so people are reached when TeamUp is closed, plus an in-app notification centre
- **Auth** — OIDC against Google Workspace, Microsoft Entra ID or any custom provider, with PKCE and refresh-token rotation; local password accounts as a fallback
- **RBAC** — roles with custom permissions
- **PWA** — installable, works on a phone
- **Observability** — structured logs, Prometheus metrics, liveness and readiness probes

---

## Getting started

### What you need

| | Version | Why |
| --- | --- | --- |
| Go | 1.25+ | Builds the server |
| Node.js | 20+ | Builds the web app (pnpm preferred, npm works) |
| Docker | with Compose v2 | Runs PostgreSQL, Redis, MinIO and LiveKit locally |

The four backing services all run as containers, so Docker is the only one you
install by hand.

### Run it

```bash
git clone https://github.com/teamup/teamup.git
cd teamup
cp .env.example .env
./start.sh
```

`start.sh` brings up PostgreSQL, Redis, MinIO and LiveKit, waits for them to
report healthy, builds the web app and the binary, applies migrations, and
starts the server. Open **http://localhost:8080** and the setup wizard will ask
for an organisation and an admin account.

The defaults in `.env.example` are development credentials and are fine for
localhost. **Change every secret before exposing this to anyone**, in particular
`TEAMUP_AUTH_SECRET`.

### Run it by hand

If you would rather not use the script:

```bash
docker compose up -d postgres redis minio livekit   # backing services
make build                                          # web app + binary
./bin/teamup migrate up                             # schema
./bin/teamup server                                 # go
```

`make build` installs frontend dependencies only when `web/node_modules` is
missing. It prefers pnpm, which owns the lockfile, but falls back to npm when
pnpm is not on `PATH` — commonly the case when pnpm was installed through nvm.

To stop everything: `./stop.sh`.

### On your phone, or another machine

```bash
./start.sh --lan
```

This works out this machine's address on the network and sets the three
settings that have to change together for it to be reachable. It will print the
address to open.

Calls and desktop notifications **will not work** over plain HTTP — browsers
only grant camera, microphone and notification access on HTTPS or `localhost`.
Chat, tasks, files, presence and search all work fine.
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) explains this in full and covers
getting HTTPS working.

---

## Configuration

Everything is configured by environment variable. `.env.example` is the
annotated list; these are the ones without which the server will not start:

```bash
TEAMUP_DATABASE_URL=postgres://teamup:teamup@localhost:5432/teamup?sslmode=disable
TEAMUP_REDIS_URL=redis://localhost:6379
TEAMUP_MINIO_ENDPOINT=localhost:9000
TEAMUP_MINIO_ACCESS_KEY=minioadmin
TEAMUP_MINIO_SECRET_KEY=minioadmin
TEAMUP_MINIO_BUCKET=teamup-files
TEAMUP_LIVEKIT_URL=http://localhost:7880
TEAMUP_LIVEKIT_API_KEY=devkey
TEAMUP_LIVEKIT_API_SECRET=devsecret
```

Commonly adjusted: `TEAMUP_PORT`, `TEAMUP_ENV`, `TEAMUP_LOG_LEVEL`,
`TEAMUP_BASE_URL`, `TEAMUP_AUTH_SECRET`. OIDC providers, SMTP and Web Push keys
are optional and can be set through the wizard instead.

Because `TEAMUP_MINIO_*` speaks S3, it points at Amazon S3, Cloudflare R2 or
Backblaze B2 just as happily as at MinIO — change the endpoint and set
`TEAMUP_MINIO_SECURE=true`.

---

## Deploying

Running TeamUp somewhere other than your own machine is covered in
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md), which starts by explaining why HTTPS
decides how much work each option is.

**A note on the current state:** the production deployment path is being
reworked. `docker-compose.prod.yml` and the Helm chart in this repository are
not yet usable as-is — there is no published container image, the chart has no
templates, and nothing runs migrations on deploy. The design for fixing this,
including a one-command install with automatic TLS, is written up in
[docs/superpowers/specs/2026-08-10-shipping-teamup-design.md](docs/superpowers/specs/2026-08-10-shipping-teamup-design.md).
Until that lands, the supported way to run TeamUp is from source as described
above. Contributions to that work are especially welcome.

---

## Testing

Most tests run against a real PostgreSQL. Create the template database once:

```bash
make build          # the migrate command lives in the binary
make test-db        # creates and migrates teamup_test
make test-unit      # or: TEAMUP_TEST_DATABASE_URL=... go test ./...
```

Each package clones that template into its own database
(`teamup_test_<package>`) and truncates between tests, so packages stay isolated
and can run in parallel. Tests that need a database **skip** when
`TEAMUP_TEST_DATABASE_URL` is unset, so `go test ./...` still works without
PostgreSQL — it just covers less.

`make lint` runs golangci-lint and eslint. `make test-e2e` runs the Playwright
suite.

---

## Contributing

TeamUp is open source and contributions are welcome from anyone — no invitation
needed, no CLA to sign. Bug reports, documentation fixes and first-time
contributions are as valuable as features.

**Good places to start:** the deployment work described above, anything in
`docs/DEPLOYMENT.md` that was unclear when you followed it, and test coverage.

### How to contribute

1. Fork the repository and create a branch off `main`.
2. Get the development environment running (see [Getting started](#getting-started)) so you can verify your change.
3. Install the commit hooks — they run the same fast checks the project expects:
   ```bash
   pip install pre-commit && pre-commit install
   ```
   This gives you gofmt, `go build`, `go vet`, `go test`, `tsc --noEmit` and
   eslint on every commit.
4. Write a test. Database-backed tests are the norm here, not the exception.
5. Commit in the style already used in the log — a conventional-commit prefix
   and a subject that says what changed for the user:
   ```
   fix: stop chat doing six round trips per message
   feat: reach people when TeamUp is closed, and wrap text that does not fit
   ```
6. Open a pull request describing the problem, not just the patch.

### Conventions

- **Issues** live as Markdown files under `.scratch/<feature>/`. See [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md).
- **Triage labels** are `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).
- **Comments explain why, not what.** The existing code comments the reasoning behind a decision and the failure it prevents; match that.
- [`AGENTS.md`](AGENTS.md) documents the conventions for AI coding agents working in this repository.

---

## Contributors and collaborators

TeamUp is currently maintained by one person. If you would like commit access
after a few merged pull requests, ask — the intent is for this to be a project
with several maintainers rather than one.

### Maintainers

| | Role |
| --- | --- |
| **Himanshu Kumar Jha** ([@himanshukrjha004](mailto:himanshukrjha004@gmail.com)) | Author and maintainer |

### Contributors

Everyone who has landed a change is listed by

```bash
git shortlog -sne
```

Add yourself to that list by opening a pull request. Thank you to everyone who
has reported a bug, tested a build, or asked an awkward question.

---

## Project structure

```
teamup/
├── cmd/teamup/           # Entry point (server, migrate, wizard, version)
├── internal/             # Private application code
│   ├── auth/             # OIDC, JWT, sessions
│   ├── calls/            # LiveKit integration
│   ├── chat/             # Channels, messages, search
│   ├── files/            # S3-compatible file storage
│   ├── mail/             # SMTP
│   ├── notifications/    # Web Push, in-app notifications
│   ├── org/              # Organisations, users, RBAC
│   ├── presence/         # Presence and availability
│   ├── tasks/            # Task management
│   ├── websocket/        # Unified WebSocket hub
│   ├── wizard/           # Setup wizard
│   └── observability/    # Logging, metrics, health
├── pkg/
│   ├── protocol/         # WebSocket message types
│   └── errors/           # Error types
├── web/                  # React + Vite + TypeScript frontend
├── migrations/           # SQL migrations (golang-migrate)
├── docs/                 # Deployment, agent conventions, design specs
└── Makefile
```

---

## API

### Auth
- `GET /auth/login/:provider` — Initiate OIDC login
- `GET /auth/callback/:provider` — OIDC callback
- `POST /auth/refresh` — Refresh access token
- `POST /auth/logout` — Logout (RP-initiated + back-channel)

### Channels
- `GET /api/channels` — List channels
- `POST /api/channels` — Create channel
- `GET /api/channels/:id` — Get channel
- `PATCH /api/channels/:id` — Update channel
- `DELETE /api/channels/:id` — Archive channel
- `POST /api/channels/:id/members` — Add member
- `DELETE /api/channels/:id/members/:userId` — Remove member

### Messages
- `GET /api/channels/:id/messages` — List messages (pagination, search)
- `POST /api/channels/:id/messages` — Send message
- `PATCH /api/messages/:id` — Edit message
- `DELETE /api/messages/:id` — Delete message
- `POST /api/messages/:id/reactions` — Add reaction
- `DELETE /api/messages/:id/reactions/:emoji` — Remove reaction

### Tasks
- `GET /api/tasks` — List tasks (with filters)
- `POST /api/tasks` — Create task
- `GET /api/tasks/:id` — Get task
- `PATCH /api/tasks/:id` — Update task
- `DELETE /api/tasks/:id` — Delete task
- `POST /api/tasks/:id/comments` — Add comment
- `POST /api/tasks/:id/attachments` — Add attachment

### Calls
- `POST /api/calls` — Create or schedule call
- `GET /api/calls/:id/token` — Get LiveKit token
- `POST /api/calls/:id/record` — Start recording
- `GET /api/calls` — List calls

### Files
- `POST /api/files/upload-url` — Get presigned upload URL
- `POST /api/files/multipart/initiate` — Initiate multipart upload
- `POST /api/files/multipart/complete` — Complete multipart upload
- `GET /api/files/download-url` — Get presigned download URL

### Notifications
- `GET /api/notifications` — List notifications
- `PATCH /api/notifications/:id/read` — Mark as read
- `PATCH /api/notifications/read-all` — Mark all as read
- `POST /api/notifications/subscribe` — Subscribe to push
- `DELETE /api/notifications/subscribe` — Unsubscribe

### Search
- `GET /api/search/messages` — Full-text search messages

### Setup wizard
- `GET /api/wizard/status` — Check setup status
- `POST /api/wizard/step1` … `step5` — Organisation, admin user, OIDC, service tests, complete

### Health and metrics
- `GET /healthz`, `GET /healthz/live` — Liveness probe
- `GET /readyz`, `GET /healthz/ready` — Readiness probe
- `GET /metrics` — Prometheus metrics

---

## WebSocket protocol

Connect to `wss://host/ws?token=<access_token>`. Every frame shares one
envelope:

```json
{
  "id": "uuid",
  "type": "chat.send",
  "payload": { "channelId": "...", "content": "..." },
  "timestamp": "2024-01-15T10:30:00Z",
  "version": 1
}
```

Message types:

- **Connection** — `conn.init`, `conn.ack`, `conn.ping`, `conn.pong`, `conn.close`
- **Chat** — `chat.send`, `chat.sent`, `chat.message`, `chat.edit`, `chat.delete`, `chat.typing`, `chat.read`
- **Presence** — `presence.update`, `presence.changed`, `presence.sync`
- **Calls** — `call.invite`, `call.invited`, `call.accept`, `call.decline`, `call.ended`, `call.signaling`, `call.signaling.relay`
- **Notifications** — `notification.new`, `notification.read`

---

## Licence

MIT — see [LICENSE](LICENSE). You may use TeamUp commercially, modify it and
distribute it; the licence and copyright notice must travel with it, and it
comes with no warranty.
