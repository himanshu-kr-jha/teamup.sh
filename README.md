<div align="center">

# TeamUp

**Chat, video calls, tasks and files — on your own server.**

A self-hosted alternative to Slack and Microsoft Teams. One Go binary with the
web app compiled into it. You run it, you own the data, there is no seat pricing.

[![CI](https://github.com/himanshu-kr-jha/teamup.sh/actions/workflows/ci.yml/badge.svg)](https://github.com/himanshu-kr-jha/teamup.sh/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black)](https://react.dev)
[![Licence](https://img.shields.io/badge/licence-MIT-green.svg)](LICENSE)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](#contributing)

[Quick start](#quick-start) · [What works](#what-works-today) ·
[Onboarding](docs/ONBOARDING.md) · [Architecture](docs/ARCHITECTURE.md) ·
[Build and deploy](docs/BUILD-AND-DEPLOY.md) · [Server setup](docs/DEPLOYMENT.md) ·
[Contributing](#contributing)

</div>

---

## Quick start

Three commands. `setup.sh` checks your tools, finds ports that are actually
free, generates real secrets, starts the four backing services and builds
everything.

```bash
git clone https://github.com/himanshu-kr-jha/teamup.sh.git
cd teamup.sh
./setup.sh && ./start.sh
```

Then open the URL it prints — **http://localhost:8080** unless that port was
taken. The first page is a setup wizard that creates your organisation and
owner account. It asks for the setup code the server prints as it starts.
There are no default credentials.

<details>
<summary><b>What <code>setup.sh</code> actually does</b></summary>

```
▸ Checking prerequisites          Go 1.26+, Node 20+, Docker with Compose v2
▸ Checking ports                  8080, 5432, 6379, 9000, 9001, 7880
                                  — moves any that are taken, and rewrites
                                    the connection strings to match
▸ Writing configuration           .env from .env.example, with a generated
                                    TEAMUP_AUTH_SECRET
▸ Starting backing services       postgres · redis · minio · livekit
▸ Creating the storage bucket     nothing else creates it; uploads fail without it
▸ Building TeamUp                 web app → Go binary (embedded)
▸ Applying database migrations
```

It is safe to run again. An existing `.env` is kept, because the auth secret in
it cannot be regenerated without signing everybody out.

`./setup.sh --check` reports what is missing and changes nothing.
`./setup.sh --force` starts over from `.env.example`.

</details>

### You will need

| | Version | Why |
| --- | --- | --- |
| **Docker** | with Compose v2 | Runs PostgreSQL, Redis, MinIO and LiveKit |
| **Go** | 1.26+ | Builds the server |
| **Node.js** | 20+ | Builds the web app (pnpm preferred, npm works) |

Docker is the only one you install by hand — the four backing services all run
as containers.

### Everyday commands

| | |
| --- | --- |
| `./setup.sh` | One-time setup, and safe to re-run |
| `./setup.sh --check` | Just tell me what's missing |
| `./start.sh` | Start everything |
| `./start.sh --lan` | Also reachable from your phone on the same Wi-Fi |
| `./stop.sh` | Stop the containers |
| `./reset.sh --yes` | Wipe all data and start from the wizard again |
| `make test-services` | Start the throwaway containers the tests run against |
| `make check` | Every check a change must pass: web, Go and browser tests |
| `make deploy` | Ship the current commit to your server ([how](docs/BUILD-AND-DEPLOY.md)) |

New here? [docs/ONBOARDING.md](docs/ONBOARDING.md) takes you from clone to your
first change in production.

---

## What works today

TeamUp is **usable but pre-1.0**. This table is checked against the code rather
than the roadmap — [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) has the
line-by-line detail.

### Solid

- **Chat** - channels, DMs, private and announcement channels, threaded
  replies, editing, deletion, reactions, read receipts, live delivery,
  forwarding with files, @mentions that notify, voice and video messages
  recorded on the spot, several files at once, history loaded a day at a time,
  unread counts per conversation and on the Chat tab, PDFs read in the
  conversation, pictures and files downloaded from where they are
- **Search** — full-text and fuzzy, over Postgres, permission-scoped and
  highlighted
- **Tasks** — create, assign along the org chart, filter, comment with
  attachments, label, watch, per-field activity log
- **Files** - presigned upload and download straight to S3, with real resumable
  multipart above 16 MiB; bytes never pass through the app
- **Calls and meetings** — LiveKit SFU, multi-party audio/video, screen share,
  voice calls from any conversation, knock to join, standing meetings with
  attendance from LiveKit's own webhooks, recording to object storage
- **Guests** - people from outside the organization join a meeting by its
  link, with their name and a passcode, and the organizer can remove them
- **Meeting notes** — a visible note-taker in every call while an administrator
  has it on, Sarvam speech to text, minutes written by Claude, OpenAI, NVIDIA or
  your own model, sent to everybody who was there and kept under Meeting notes
- **Calendar** — month, week and day views, focus time, open slots,
  meetings with a length from a list, availability across people,
  meetings that run over shown as extended, and managers reading the
  calendars of the people on their teams
- **Email** — mail server set up by an administrator, welcome email with
  sign-in details for every new member
- **Presence** — online / away / in-call, availability and leave, with keys
  that expire so a crashed process does not leave people showing online
- **Auth** — password login, RS256 JWTs with a persisted signing key, refresh
  rotation with reuse detection, custom roles and RBAC
- **Admin** — people, roles and a team tree, gated on both client and server;
  the organization's own colour and logo; profile pictures for everyone
- **Status page** — `/status`, public, with 24 hours of history and a banner in
  the app while something is down
- **App on your phone** - installs to an Android or iPhone home screen and opens full screen; Android installs from a suggestion or Settings at a tap, an iPhone is shown the Share, Add to Home Screen steps (where notifications on an iPhone have to come from); icons drawn by `web/scripts/make-icons.mjs`
- **Welcome tour** - a look around at first sign-in, a spotlight on each place, then turning on notifications and a welcome; once per person, kept by the server, and shown again from Settings
- **Ops** — structured logs, Prometheus metrics, an honest `/readyz`, graceful
  shutdown that drains in-flight requests

### Known gaps

Being straight about these, because discovering them yourself is worse:

| | |
| --- | --- |
| **OIDC sign-in is a stub** | The Google / Entra / custom buttons complete the OAuth round-trip but provision a fabricated in-memory user. **Password login is the real path.** ([#15](https://github.com/himanshu-kr-jha/teamup.sh/issues/15)) |
| **No typing indicators** | The socket handshake works, so typing is unblocked, but nothing in the UI sends or renders it yet. ([#28](https://github.com/himanshu-kr-jha/teamup.sh/issues/28)) |
| **Offline mode is aspirational** | Installable service worker, yes; IndexedDB outbox, no ([#29](https://github.com/himanshu-kr-jha/teamup.sh/issues/29)) |
| **`/metrics` is unauthenticated** | Fine on a laptop, do not publish it. The shipped `nginx.conf` refuses it from outside ([#20](https://github.com/himanshu-kr-jha/teamup.sh/issues/20)) |
| **No login rate limit in the app** | The shipped `nginx.conf` applies one. Anything else in front of TeamUp has to bring its own |
| **Recording is not in the production stack** | `docker-compose.prod.yml` has no LiveKit Egress service, so calls work and recording does not |

Nothing here is architectural — most are small, well-scoped fixes. The open
ones are tracked as
[GitHub issues](https://github.com/himanshu-kr-jha/teamup.sh/issues), and
[the architecture doc ends with the ones worth doing first](docs/ARCHITECTURE.md#if-you-want-to-contribute).

### Not built

Federation · end-to-end encryption · native mobile apps · guest access ·
retention policies and compliance export · i18n

---

## How it fits together

```
                            ┌──────────────────────────┐
   browser                  │      teamup (binary)     │
  ┌─────────┐   HTTP/REST   │                          │
  │  React  │◄─────────────►│  gin router              │
  │   SPA   │               │    ├── /api/*   handlers │
  │  (PWA)  │◄─────────────►│    ├── /ws      hub      │
  └────┬────┘   WebSocket   │    └── /*       embedded │
       │                    │                 web/dist │
       │                    └───┬───┬───┬──────────────┘
       │                        │   │   │
       │                  ┌─────┘   │   └──────┐
       │                  ▼         ▼          ▼
       │            ┌──────────┐ ┌──────┐ ┌─────────┐
       │            │ Postgres │ │Redis │ │  MinIO  │
       │            └──────────┘ └──────┘ └─────────┘
       │                                       ▲
       │            ┌──────────┐               │ recordings
       └───────────►│ LiveKit  │───────────────┘
         media      └──────────┘
```

The web app is compiled **into** the binary with `go:embed`, so the API and the
UI are always the same version and there is no static file server to configure.
Media and file bytes bypass the app entirely — the browser talks to LiveKit and
S3 directly, and TeamUp just signs the tokens.

Full detail, including request paths and what breaks when each service dies:
**[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)**.

### On phones

TeamUp installs from the browser as an app on any phone.
There is also a store app for Android, with iPhone to follow, in [mobile/](mobile/README.md): a native shell that opens the web app of your server, so it is always the same version as the website.

### Watch it under load

**[Open TeamUp in Breakscale][breakscale-teamup]**, a system design simulator that runs in the browser.
Press play, then drag the traffic on "Office: 250 busy people" up and watch where it breaks.

The design is the production stack on one 2-CPU server, and its numbers come from the load test in [tools/loadtest](tools/loadtest/README.md).
It breaks where the real server did:

| Office load | Real server | Breakscale |
| --- | --- | --- |
| 250 busy people, 110 requests a second | fine | p50 10 ms |
| 500 busy people, 220 requests a second | p50 70 ms, app CPU used up | p50 78 ms, app 100% busy |
| more | overloaded | requests turned away, the app runs out first |

It also shows live connections, files in MinIO, LiveKit with room for about 100 people in calls, and the note-taker's queues to speech-to-text and the minutes model.
The times for MinIO, Sarvam and the minutes model are assumed rather than measured.
The same design is in [docs/teamup.breakscale](docs/teamup.breakscale), which opens on breakscale.tech and in its VS Code extension.

[breakscale-teamup]: https://breakscale.tech/#d3.AU3RzU7bQBAHcGfZ2E5CgkNJCP1iqAqyKxAJKlLVQ9WqFRUqAZdAe944m7Cts2t510Bv-wh5hDxCHwEuOVd9Aj-CTz1XDlTqcVYz-u38Z3XZMHy_dTIcsoC-hr39NvQT-QMiKqKQorRaSLFv8hHj16bW9gbK7Gxh9YyS8XkEJIrA_Si2oQPv_XOv5twWNSzoaorWTumASXBDNmZKbkMUU0l5QL2ao-2Ngq5muHlKyUDCq_1N2AVJ-UBCZ38TG2hS1-ZSj_IBEBhTKcmImtrewNOGtuq-kGoUUwnunfnmd9lxbrG2G9NGhmt3bJT0d2XSN9DPNW0-OWKXFC6SPrhRIi9ACRAR5aBIX3qmtj8bforKJ_-e0NtmilO0Op8KBOc0UExwCe7ulfTM2eaW4Wf40QELKSRRKPIdCB_AQFzxeYX8Wooz3OoyfngCQxbmn71kBOYheuXZ4g0yfG01_HnE8E0wzvgIAhKGEjnNFGtrPdc_MQW9g3NwSV8kCjrt9v1VPHNmNQx_UnraiygNLiAIWSRhGIsxdPZgTKlifCQRKaZ4UqofC0V3FPlO47tGw8xsbbbOYsJlELN-jqskzlfs7HnmTcFBuqqtZo_El2QMcm7sKLGj6LUytb310J9WnO69ApQPGB-ZM6tdTPG0Uu8ynigq85yvYqZozk3s5a8xU_9JLz2cM5PS2tFRF4YihvH9nHv85fDD4bv8NH8KBipgZCIbV9GvMq6grFBcLNYsp7RSaVUfL4HzrP78gbvyorH9Fw

<details>
<summary><b>Project layout</b></summary>

```
teamup/
├── cmd/teamup/           # Entry point and composition root
├── internal/             # Private application code
│   ├── auth/             #   OIDC, JWT, sessions, signing key
│   ├── calls/            #   LiveKit integration
│   ├── chat/             #   Channels, messages, search
│   ├── files/            #   S3-compatible storage
│   ├── mail/             #   SMTP
│   ├── notifications/    #   Web Push, in-app centre
│   ├── observability/    #   Logging, metrics, health
│   ├── org/              #   Organisations, users, RBAC
│   ├── presence/         #   Presence and availability
│   ├── tasks/            #   Task management
│   ├── websocket/        #   The connection hub
│   └── wizard/           #   First-run setup
├── pkg/protocol/         # WebSocket message types
├── web/                  # React + Vite + TypeScript
├── migrations/           # SQL migrations (golang-migrate)
├── helm/teamup/          # Kubernetes chart
└── docs/                 # Architecture, deployment, conventions
```

Each domain package is `routes.go` (HTTP shape) plus a domain file (rules,
authorization and SQL). There is deliberately no separate store layer.

</details>

---

## Configuration

Everything is an environment variable; `.env.example` is the annotated list.
Run `./setup.sh` rather than copying it by hand — the example ships a
placeholder auth secret, and copying it verbatim leaves that in place.

The server **fails at boot**, not on first request, if any of these is missing:

```bash
TEAMUP_DATABASE_URL      TEAMUP_MINIO_ENDPOINT     TEAMUP_LIVEKIT_URL
TEAMUP_REDIS_URL         TEAMUP_MINIO_ACCESS_KEY   TEAMUP_LIVEKIT_API_KEY
                         TEAMUP_MINIO_SECRET_KEY   TEAMUP_LIVEKIT_API_SECRET
                         TEAMUP_MINIO_BUCKET
```

That means you cannot start TeamUp to try only chat — all four services must at
least be configured.

> **`TEAMUP_AUTH_SECRET` is the one to back up.** It encrypts the key TeamUp
> signs tokens with. Once a deployment has started with a value, keep it: the
> stored key cannot be read back without it, and losing it signs everybody out.

Because storage is plain S3, `TEAMUP_MINIO_*` points just as happily at Amazon
S3, Cloudflare R2 or Backblaze B2 — change the endpoint and set
`TEAMUP_MINIO_SECURE=true`. On a server, see
[Files in an outside bucket](docs/DEPLOYMENT.md#files-in-an-outside-bucket).

<details>
<summary><b>Running on your phone or another machine</b></summary>

```bash
./start.sh --lan
```

This finds this machine's network address and sets the three settings that have
to change together, then prints the URL to open.

**Calls and desktop notifications will not work over plain HTTP** — browsers
only grant camera, microphone and notification access on HTTPS or `localhost`.
Chat, tasks, files, presence and search are all fine.
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) covers getting HTTPS working.

</details>

<details>
<summary><b>Ports, and what to do when one is taken</b></summary>

| Port | Service |
| --- | --- |
| 8080 | TeamUp |
| 5432 | PostgreSQL |
| 6379 | Redis |
| 9000 / 9001 | MinIO / its console |
| 7880-7882, UDP 50000-60000 | LiveKit |

`setup.sh` detects conflicts and moves the host-side port, writing the new value
into `.env` along with the matching connection string. Inside the Docker network
the services always use their standard ports.

One catch: if **LiveKit** has to move, the frontend needs `VITE_LIVEKIT_URL` in
`web/.env` *before it is built*, because over plain HTTP the browser dials
LiveKit directly and otherwise assumes 7880. `setup.sh` writes that file for you.

</details>

<details>
<summary><b>Running it by hand, without the scripts</b></summary>

```bash
docker compose up -d postgres redis minio livekit   # backing services
make build                                          # web app + binary
./bin/teamup migrate up                             # schema
./bin/teamup server                                 # go
```

`make build` installs frontend dependencies only when `web/node_modules` is
missing. It prefers pnpm, which owns the lockfile, but falls back to npm when
pnpm is not on `PATH` — commonly the case when pnpm was installed through nvm.

You will also need to create the MinIO bucket yourself; nothing in the boot path
does it.

</details>

---

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `.env not found` | Run `./setup.sh` first |
| Compose fails to bind a port | Something else has it — `./setup.sh --force` picks free ones |
| `/readyz` says `minio: bucket does not exist` | The bucket was never created; `./setup.sh` does it, or make it in the MinIO console |
| Server exits at boot naming a variable | A required setting is missing — see [Configuration](#configuration) |
| Blank page, or "frontend not built" | `make build` — the binary refuses to start without an embedded `index.html` |
| Camera/mic blocked off localhost | Browsers require HTTPS for media. See [DEPLOYMENT.md](docs/DEPLOYMENT.md) |
| Signed out after changing `.env` | `TEAMUP_AUTH_SECRET` changed; the stored signing key can no longer be decrypted |
| Tests all pass suspiciously fast | They skipped. Use `make test-services && make test-go` |

---

## Testing

Most tests run against real services: Postgres, Redis, MinIO and LiveKit in
throwaway containers, and the browser tests against the real binary.

```bash
make test-services   # once: start the test containers, migrate the test database
make test-web        # ESLint, TypeScript, Vitest
make test-go         # gofmt, go vet, every Go test against the containers
make e2e             # rebuild, then every Playwright test against the real stack
make check           # all of the above
```

Each Go package clones a migrated template database into its own and truncates
between tests, so packages stay isolated and run in parallel.

> Database tests **skip** when `TEAMUP_TEST_DATABASE_URL` is unset. A run can
> be entirely green having tested almost nothing, which is exactly how a
> completely dead WebSocket path once survived 286 passing tests. CI fails the
> build if any test reports SKIP, and `make test-go` always sets it.

[docs/ONBOARDING.md](docs/ONBOARDING.md#4-the-tests) has the details, including
running one browser test and reading a failure.

---

## Deploying

[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) is a copy-paste walkthrough from a
fresh Ubuntu VM to a running team, with sizing, backups, upgrades and the
alternatives compared. After that, every release is `make deploy`:
[docs/BUILD-AND-DEPLOY.md](docs/BUILD-AND-DEPLOY.md) covers the flow, what
people notice, moving to a new address and rolling back.

- **One VM with Docker Compose** - `docker-compose.prod.yml`, the recommended
  path. It runs all six services, and it is what the guide was tested against.
  Files can live in an S3 or Lightsail bucket instead of the MinIO container,
  so they survive losing the server:
  `scripts/new-server.sh --files-bucket <name> --files-region <region>` sets it
  up. See [Files in an outside bucket](docs/DEPLOYMENT.md#files-in-an-outside-bucket).
- **Kubernetes** - `helm/teamup`, which renders the deployment, service,
  ingress, config, secrets and a migration `pre-install` hook. The templates
  render and validate, but **have not yet been installed against a live
  cluster**. Treat the chart as beta and expect to adjust LiveKit's networking.
- **A binary and a systemd unit** - it is one file; this is a legitimate option

Outside Compose, Postgres, Redis, S3 and LiveKit are prerequisites you bring.

---

## API

<details>
<summary><b>REST endpoints</b></summary>

**Auth** — `POST /api/auth/login`, `POST /api/auth/refresh`,
`POST /api/auth/logout`, `GET /api/auth/login/:provider`,
`GET /api/auth/callback/:provider`, sessions list/revoke, change password

**Channels** — `GET|POST /api/channels`, `GET|PATCH|DELETE /api/channels/:id`,
member add/remove

**Messages** — `GET|POST /api/channels/:id/messages`,
`PATCH|DELETE /api/messages/:id`, `POST /api/messages/:id/forward`, reactions,
read markers

**Tasks** — `GET|POST /api/tasks`, `GET|PATCH|DELETE /api/tasks/:id`, comments,
attachments, labels, watchers

**Calls** — `GET|POST /api/calls`, `GET /api/calls/:id/token`,
`POST /api/calls/:id/record`

**Files** — `POST /api/files/upload-url`, `GET /api/files/download-url`,
`POST /api/files/record`

**Notifications** — `GET /api/notifications`, mark read / read-all, push
subscribe / unsubscribe, `GET /api/notifications/vapid-key`

**Search** — `GET /api/search/messages`, `GET /api/search/files`

**Calendar and meetings** - `GET /api/calendar`, `GET /api/calendar/people`, `GET /api/calendar/availability`,
blocks, `POST|PATCH|DELETE /api/meetings/:id`, standing meetings and attendance

**Organization** — users, roles, teams, `GET|PUT /api/org/mail`,
`POST /api/org/mail/test`, `PUT /api/org/branding`,
`GET|PUT|DELETE /api/users/:id/avatar`

**Public, no session** — `GET /api/status`, `GET /api/branding`,
`GET /api/branding/logo`

**Wizard** — `GET /api/wizard/status`, `POST /api/wizard/step1` … `step5`

**Ops** — `GET /healthz`, `GET /readyz`, `GET /metrics`

</details>

<details>
<summary><b>WebSocket protocol</b></summary>

Connect to `/ws`; the HttpOnly `at` cookie authenticates the upgrade. Every
frame shares one envelope:

```json
{
  "id": "uuid",
  "type": "chat.send",
  "payload": { "channelId": "...", "content": "..." },
  "timestamp": "2026-08-13T10:30:00Z",
  "version": 1
}
```

- **Connection** — `conn.init`, `conn.ack`, `conn.ping`, `conn.pong`, `conn.close`
- **Chat** — `chat.send`, `chat.sent`, `chat.message`, `chat.edit`,
  `chat.delete`, `chat.typing`, `chat.read`
- **Presence** — `presence.update`, `presence.changed`, `presence.sync`
- **Calls** — `call.invite`, `call.invited`, `call.accept`, `call.decline`,
  `call.ended`, `call.signaling`
- **Notifications** — `notification.new`, `notification.read`

The client sends `conn.init` and the server answers `conn.ack`, after which the
socket carries traffic both ways. See
[the architecture doc](docs/ARCHITECTURE.md#the-websocket).

</details>

---

## Contributing

Contributions are welcome from anyone — no invitation needed, no CLA. Bug
reports, documentation fixes and first-time contributions are as valuable as
features.

**Good places to start:** the five items at the end of
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#if-you-want-to-contribute), anything
in the setup flow that was unclear when you followed it, and test coverage.

1. Fork, and branch off `main`.
2. Get it running and the tests passing:
   [docs/ONBOARDING.md](docs/ONBOARDING.md) walks through it, and through
   making a change from ticket to production.
3. Install the commit hooks — the same fast checks the project expects:
   ```bash
   pip install pre-commit && pre-commit install
   ```
   That gives you gofmt, `go build`, `go vet`, `go test`, `tsc --noEmit` and
   eslint on every commit.
4. **Write a test.** Database-backed tests are the norm here, not the exception.
   If it touches org-scoped data, put a second organisation in the test — a
   single-org test cannot catch a missing `org_id` filter.
5. Commit in the style already in the log — a conventional-commit prefix and a
   subject saying what changed for the user:
   ```
   fix: stop chat doing six round trips per message
   feat: reach people when TeamUp is closed, and wrap text that does not fit
   ```
6. Open a pull request describing the problem, not just the patch.

### Conventions

- **Issues** live as Markdown under `.scratch/<feature>/` — see
  [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md)
- **Triage labels** are documented in
  [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md)
- **Comments explain why, not what.** The existing code comments the reasoning
  behind a decision and the failure it prevents; match that.
- [`AGENTS.md`](AGENTS.md) documents the conventions for AI coding agents.

### Maintainers

TeamUp is currently maintained by one person. If you would like commit access
after a few merged pull requests, ask — the intent is several maintainers rather
than one.

| | Role |
| --- | --- |
| **Himanshu Kumar Jha** ([@himanshukrjha004](mailto:himanshukrjha004@gmail.com)) | Author and maintainer |

Everyone who has landed a change is listed by `git shortlog -sne`. Thank you to
everyone who has reported a bug, tested a build, or asked an awkward question.

---

## Licence

MIT — see [LICENSE](LICENSE). Use it commercially, modify it, distribute it; the
licence and copyright notice travel with it, and it comes with no warranty.
