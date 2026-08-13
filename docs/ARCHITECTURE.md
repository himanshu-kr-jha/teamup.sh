# TeamUp architecture

A map of how TeamUp is put together, what each part is responsible for, and —
just as importantly — **what is not built yet**. If you are about to change
something, this is the page that tells you which door to go through.

Every claim here was checked against the code, not against the ticket tracker.
Where a feature is half-built, it says so and cites the line.

For getting it running see the [README](../README.md); for running it somewhere
other than your laptop see [DEPLOYMENT.md](DEPLOYMENT.md).

---

## The shape of it

TeamUp is **one Go binary** with the React app compiled into it, talking to
**four backing services**. No separate web server, no app server, no job
runner, no message queue. You deploy a file.

```
                            ┌──────────────────────────┐
   browser                  │      teamup (binary)     │
  ┌─────────┐   HTTP/REST   │                          │
  │  React  │◄─────────────►│  gin router              │
  │   SPA   │               │    ├── /api/*   handlers │
  │  (PWA)  │◄─────────────►│    ├── /ws      hub      │
  └────┬────┘   WebSocket   │    ├── /livekit proxy    │
       │                    │    └── /*       embedded │
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
         (WebRTC, direct)
```

Three things in that picture are easy to miss and matter a lot:

**Media never touches the Go binary.** The browser negotiates with LiveKit
directly over WebRTC. TeamUp only mints a signed join token. A call can be
working while the app struggles — and can break in ways the server log knows
nothing about.

**File bytes never touch the Go binary either.** Uploads and downloads use
presigned S3 URLs; the browser talks to MinIO directly. TeamUp signs the URL and
records the metadata row.

**Chat is sent over REST and received over the WebSocket.** That split is not
an accident of history, and it is worth internalising before you go looking for
a send path on the socket. See [The WebSocket](#the-websocket), which also
records why the inbound half of that socket was dead for most of this
project's life — the most surprising thing in the codebase.

---

## Request paths

### A REST call

```
browser
  → gin router                  cmd/teamup/main.go:480    Recovery → GinLogger → GinMetrics
  → /api group                  cmd/teamup/main.go:542
  → auth middleware             internal/auth/auth.go:488  RS256 JWT from the "at" cookie;
                                                           silently refreshes from "rt" if expired;
                                                           puts user_id / org_id / roles in context
  → handler                     internal/<domain>/routes.go
  → service                     internal/<domain>/<domain>.go   rules, authorization, SQL
  → PostgreSQL (pgx/v5)
```

**There is no repository or store layer.** Each service holds a `*pgxpool.Pool`
and writes SQL inline. If you are looking for where a query lives, it is in the
domain file, not a `store.go`. This is a deliberate choice — one fewer
indirection — but it does mean authorization and persistence sit side by side,
so read carefully before editing a query.

### A chat message

Sending is REST; receiving is WebSocket. That split surprises people, and for
most of this project's life it was also *why chat worked at all* — the inbound
socket path was dead, and REST did not depend on it:

```
sender    POST /api/channels/:id/messages ──► chat.SendMessage      chat.go:1535
                                                   │
                                                   ├──► Postgres (messages)
                                                   │
                                                   ├──► hub.BroadcastToChannel  hub.go:220
                                                   │         └──► chat.message ──► recipients
                                                   │
                                                   └──► notifications ──► in-app row
                                                                      └──► web push
```

### The WebSocket

One connection per browser tab, carrying chat, presence, calls and
notifications over a single envelope:

```json
{
  "id": "uuid",
  "type": "chat.send",
  "payload": { "channelId": "...", "content": "hello" },
  "timestamp": "2026-08-13T10:30:00Z",
  "version": 1
}
```

Auth is by cookie — the browser attaches the HttpOnly `at` cookie on the
upgrade request; there is no token in the URL. Origin is checked against
same-origin plus `TEAMUP_ALLOWED_WS_ORIGINS` (`hub.go:481`).

`payload` is `json.RawMessage` in
[`pkg/protocol/messages.go`](../pkg/protocol/messages.go), decoded by whichever
handler owns the `type`.

> **Do not change this back to `interface{}`.** It used to be, and
> `encoding/json` fills an `interface{}` with `map[string]interface{}`, never
> `[]byte`. Every handler then did `payload.([]byte)`, the assertion failed, and
> the handler returned before reaching a single log line. The whole inbound
> path was dead and silent.

**The handshake works, as of the authorization/real-time wave.** It did not
until recently, and the history is worth knowing because it explains the shape
of the code: the server had no case for `conn.init` and never emitted
`conn.ack`, so the client's state never reached `'ready'` and everything passed
to `send()` queued forever. The socket was downstream-only. The client's queue
flush on `conn.ack` had been written all along — it was simply unreachable.

What that leaves today:

- `chat.send` over the socket now works, though the shipped UI still uses REST
- `chat.typing` is unblocked on the wire, but **nothing in the UI sends or
  renders it yet** — the server handler has been correct throughout
- Connections carry a last-heard-from stamp, and the client answers
  `conn.ping` with `conn.pong`, which is what makes reaping safe

**Locking.** The hub keeps `map[userID]map[connID]*Connection`. Every broadcast
copies the target connections into a slice **while holding the lock** and sends
afterwards. Iterating the map without the lock is `fatal error: concurrent map
iteration and map write` — a Go runtime throw that `gin.Recovery` cannot catch,
so it kills every session on the process, not just the one that tripped it.

---

## Packages

| Package | Lines | What it owns |
| --- | --- | --- |
| `internal/chat` | ~5,200 | Channels, messages, reactions, search |
| `internal/tasks` | ~3,200 | Tasks, comments, labels, watchers, activity |
| `internal/auth` | ~3,100 | Passwords, OIDC plumbing, JWT, refresh rotation, signing key |
| `internal/calls` | ~2,700 | LiveKit rooms, join tokens, recordings, call events |
| `internal/org` | ~2,400 | Organisations, teams, users, roles, RBAC |
| `internal/wizard` | ~1,800 | First-run setup, stored configuration |
| `internal/websocket` | ~1,100 | The connection hub, read/write loops |
| `internal/files` | ~1,300 | Presigned upload/download |
| `internal/notifications` | ~1,100 | Web Push (VAPID) and the in-app centre |
| `internal/observability` | ~830 | Logging, metrics, health probes |
| `internal/presence` | ~540 | Online/away/in-call, availability |
| `internal/mail` | ~300 | SMTP |
| `internal/testsupport` | ~300 | Template-database cloning for tests |
| `pkg/protocol` | — | WebSocket envelope and message types |
| `pkg/errors` | — | Shared error types |

`internal/` is private by Go's own rules. `pkg/` is the deliberate public
surface.

[`cmd/teamup/main.go`](../cmd/teamup/main.go) is the whole composition root —
config, pools, services, routes, middleware order, shutdown. It is long on
purpose: one file you read top to bottom, rather than wiring scattered across a
dozen `init()` functions. Subcommands: `server`, `migrate up|down|create`,
`version`.

---

## The four backing services

All four must be **configured** or the binary refuses to boot — you cannot start
TeamUp to try only chat. What differs is what happens when one dies afterwards.

| Service | Used for | If it goes down at runtime |
| --- | --- | --- |
| **PostgreSQL** | Everything durable: accounts, messages, tasks, file metadata, notifications, refresh tokens, and the JWT signing key | Total outage. Boot also fails hard (`main.go:370`, `main.go:388`). |
| **Redis** | Only two things: presence hashes and the wizard's setup session. `auth`, `chat` and `notifications` hold a client they never use. | Presence dots and the online list stop; an in-progress wizard loses its session. **Chat, tasks, calls and notifications keep working.** |
| **MinIO** (or any S3) | Attachments, avatars, call recordings | `/readyz` goes 503; uploads and downloads fail. Messages, tasks, calls and search carry on. |
| **LiveKit** (+ Egress) | WebRTC SFU for audio, video, screen share | `/readyz` goes 503; calls can be created and ring, but joining fails. Everything else unaffected. |

`/readyz` checks all four concurrently under a 2-second deadline, caches for 2
seconds, and names the one that failed. `/healthz` deliberately checks
**nothing** — 200 while the process lives. That asymmetry is intentional: if
liveness depended on Postgres, a brief blip would make Kubernetes *restart every
pod* instead of just removing them from the load balancer.

Because storage is plain S3, `TEAMUP_MINIO_*` points equally at Amazon S3,
Cloudflare R2 or Backblaze B2 — change the endpoint, set
`TEAMUP_MINIO_SECURE=true`.

The bucket is created during `FinalizeSetup` and on the auto-setup path, before
the provisioning transaction opens, so a storage failure leaves no
half-provisioned organisation. `./setup.sh` also creates it directly.

> This was not true until recently, and it is the kind of gap worth
> recognising: nothing on the boot path created the bucket, and
> `ValidateServices` — which would have — was dead code whose only reference
> was its own definition. An instance provisioned by anything other than a
> human clicking the wizard's "Test connection" button came up with `/readyz`
> reporting MinIO unhealthy and every upload failing, announced nowhere.

---

## Data model

32 tables across 11 migrations, applied with
[golang-migrate](https://github.com/golang-migrate/migrate):

- **Tenancy** — `organizations`, `org_configs`, `teams`, `team_members`
- **Identity** — `users`, `roles`, `user_roles`, `linked_identities`,
  `refresh_tokens`, `signing_keys`
- **Chat** — `channels`, `channel_members`, `channel_departures`, `messages`,
  `message_reactions`, `attachments`
- **Tasks** — `tasks`, `projects`, `sprints`, `sprint_tasks`, `labels`,
  `task_labels`, `task_comments`, `task_attachments`, `task_activities`,
  `task_watchers`
- **Calls** — `calls`, `call_participants`
- **Other** — `notifications`, `push_subscriptions`, `user_presence`,
  `audit_logs`

Two things to know before writing a query:

**Everything is scoped by `org_id`.** TeamUp is multi-tenant even when you run
one organisation. A query that forgets the org filter is a data leak, not a bug
— and it will pass every single-org test you write. **Put a second org in the
test.**

**Search is Postgres, not Elasticsearch.** Messages carry a generated `tsvector`
with a GIN index, `ts_headline` for highlighting, and a trigram fuzzy fallback
(migration `0006`). One fewer service to run.

Note that `projects`, `sprints` and `sprint_tasks` exist in the schema but the
board/sprint features on top of them are deliberately out of scope for v1.

---

## Authentication

Access tokens are **RS256 JWTs**. The private key lives in `signing_keys`,
encrypted at rest with `TEAMUP_AUTH_SECRET`.

It is stored rather than generated at boot because an RSA key cannot be derived
deterministically from a seed — Go's `crypto/rsa` consumes a nondeterministic
amount of entropy. Two replicas each generating their own would reject each
other's tokens, and every restart would sign everyone out. The first process to
start creates the key under a `pg_advisory_xact_lock` and re-reads inside the
transaction, so ten replicas booting at once against an empty database still
agree on one key.

> **`TEAMUP_AUTH_SECRET` is not decorative.** Lose it and the stored key cannot
> be decrypted: every session dies and the key must be regenerated. Back it up
> with the same care as the database.

Refresh tokens rotate on use with reuse detection per the OAuth 2.0 Security
BCP — presenting an already-used token invalidates the whole family, on the
assumption it was stolen. A short grace path handles the loser of a concurrent
refresh (`auth.go:373`).

**Password login is the only working sign-in.** See the status table below.

---

## Frontend

React 18 + TypeScript + Vite in `web/`. TanStack Query for server state, Zustand
for local state, Tailwind and Radix for UI, `vite-plugin-pwa` for the service
worker.

Vite writes to `cmd/teamup/web/dist`, and `make build` then compiles the binary
with `//go:embed all:web/dist` — the SPA ships *inside* the executable. No
static file server to configure, and no version skew between API and UI: they
are the same artifact. The binary refuses to start if that directory has no
`index.html` (`main.go:533`), so you get a loud error rather than a blank page.

One consequence: **changing frontend code needs a Go rebuild** to see it in the
binary. For day-to-day work run Vite's dev server instead
(`cd web && npm run dev`).

---

## What works, and what does not

### Solid

| Area | Notes |
| --- | --- |
| Password auth, sessions, RBAC | bcrypt, RS256, device list, revoke, change-password revokes all |
| Refresh rotation | Transactional, `FOR UPDATE`, reuse detection |
| Channels | Public / private / DM / announcement, auto-enrol, archive, membership |
| Messages | Send, edit (5-min window), delete, live fan-out to the channel |
| Search | Postgres FTS + trigram, ranked and highlighted, permission-scoped |
| Read receipts | Markers, "read by", counts ride along with history |
| Tasks | Create, filter, comment, label, watch, per-field activity log |
| Files | Presigned upload/download, double access check, hourly sweep of orphans |
| Calls | Create, ring, join with scoped token, decline, mute, end; recording to MinIO |
| In-app notifications | Real-time bell, unread counts, mark-read, archive |
| Org admin | People, custom roles, team tree — gated client- *and* server-side |
| First-run wizard | Org + owner account, advisory-locked idempotent finalize |
| Observability basics | zerolog JSON, honest `/readyz`, graceful shutdown drain |

### Half-built — works, with a specific catch

| Thing | The catch |
| --- | --- |
| **Web push** | Real delivery, keys auto-generated by the wizard. Silently no-ops without keys; HTTPS-only in the browser. |
| **Task updates** | Deletion is now gated on authority over the reporter, but **any member can still retitle or restatus any task**. That is arguably the product's model — a shared board — and unlike deletion, every field change writes an activity row naming the actor. `RemoveLabel` is the one mutation that is both ungated and untraced. |
| **Multipart upload** | Fictional. `InitiateMultipart` forwards to `GetUploadURL`; `CompleteMultipartUpload` is a no-op. Large files still work via a single presigned PUT. |
| **Correlation IDs** | Helpers exist; no middleware calls them. No request carries one. |
| **Typing indicators** | Unblocked on the wire now that the handshake completes, and the server handler is correct — but no UI sends or renders them. |
| **PWA offline** | Installable with a service worker, but the offline half is aspirational: no IndexedDB, no outbox, no BackgroundSync. `idb` is a dependency that is imported nowhere; `OfflineBanner` hard-codes 0. Runtime caching rules are anchored to `^https://` so none match on localhost. |
| **Wizard OIDC step** | Settings are written to `org_configs` but the auth service only reads providers from **environment variables at boot**. Configuring OIDC in the wizard does not enable it. |

### Stubs — present in the UI or API, not actually implemented

| Thing | Reality |
| --- | --- |
| **OIDC sign-in** (Google / Entra / custom) | The OAuth round-trip completes, but `findOrCreateUser` (`internal/auth/routes.go:442`) returns a **fabricated in-memory user** with `uuid.New()` for both ID and OrgID. You get a valid session for a user that exists nowhere and every screen is empty. Use password login. |
| **Back-channel logout** | Parses the JWT **without verifying the signature**, revokes nothing, returns 200 |
| **WS call signalling** | `HandleCallSignaling` unmarshals the payload and discards it. Real signalling is browser↔LiveKit direct. |
| **Thumbnails** | `GenerateThumbnail` returns `"", nil` and has no callers |
| **Threads** | `replyTo` is stored and round-tripped, but there is no thread endpoint and no thread view |

### Security items to know about

- **`call.accept` trusts the room name it is given.** `HandleCallAccept`
  (`calls.go:679`) mints a publish/subscribe LiveKit token for whatever room
  the client names, with no check that the call exists, that the caller was
  invited, or that it belongs to their organisation. Room names are
  `"call-" + uuid[:8]` — 32 bits, enumerable. The shipped UI never sends this,
  which is the only thing limiting it.
  ([#34](https://github.com/himanshu-kr-jha/teamup.sh/issues/34))
- `/metrics` is unauthenticated and outside every auth group (`main.go:507`).
  Fine on a laptop; **do not publish it** — it exposes user counts, message and
  call volumes, and per-route error rates.
- Back-channel logout parses its token **without verifying the signature**,
  revokes nothing, and returns 200.

Recently closed, and worth knowing as a pattern: cross-org call invites minted
LiveKit tokens for users in other organisations, and task deletion was gated on
nothing but org membership. Both were invisible to single-tenant tests. **When
you write a test that touches org-scoped data, put a second organisation in it
— and where the rule is about people rather than tenants, a second member.**

### Not built at all

Federation between instances · end-to-end encryption · native mobile apps (the
PWA is the mobile story) · guest/external access · message retention and
compliance export · calendar · i18n · AI features · Grafana dashboards and alert
rules (the `ServiceMonitor` ships; the dashboards do not) · a Helm chart
verified against a real cluster (templates render and `kubeconform` validates,
but nobody has installed it live).

---

## Testing

Most tests run against a real PostgreSQL — no mocks for the database layer.
`internal/testsupport` migrates one template database; each package clones it,
so packages stay isolated and run in parallel.

```bash
make build      # migrate lives in the binary
make test-db    # create and migrate the template
make test-unit  # go test -race
```

**The trap:** database tests **skip** when `TEAMUP_TEST_DATABASE_URL` is unset.
Without it the large majority of tests skip and the run still prints `ok`.
This is not
hypothetical — 286 tests passed with zero failures while the entire inbound
WebSocket path was dead, because nothing ever sent a frame. CI therefore fails
the build if any test reports SKIP.

With Postgres and Redis: **458 pass (378 top-level), 0 skip, 0 races.**

Two tests in `cmd/teamup` additionally need a live MinIO and LiveKit, because
they boot a real server — they fail without them. CI provides all four.

When you write a test that touches org-scoped data, **create a second
organisation in it**. A single-org test cannot detect a missing `org_id` filter.

---

## Deploying

- **Docker Compose** — `docker-compose.prod.yml`
- **Kubernetes** — `helm/teamup`, rendering 6 resources (8 with ingress and
  autoscaling), with migrations as a `pre-install` hook since the server never
  migrates on boot
- **A binary and a systemd unit** — it is one file; this is a legitimate option

Postgres, Redis, S3 and LiveKit are prerequisites in every case, not bundled.
LiveKit in particular needs cluster-specific UDP and external-address
configuration that no chart default gets right.

`docker-compose.yml` pins container names, so **two checkouts cannot run side by
side** without editing it.

[DEPLOYMENT.md](DEPLOYMENT.md) has the detail, starting with why HTTPS decides
how much work each option is.

---

## If you want to contribute

Good pieces of work, in rough order of value for effort:

1. **Scope `call.accept` to a real call** ([#34](https://github.com/himanshu-kr-jha/teamup.sh/issues/34))
   — the HTTP token route already does this correctly, so it is a matter of
   making the WebSocket handler agree with it. Highest severity open item.
2. **Wire up typing indicators** — the socket handshake and the server handler
   are both done; only the UI is missing. Small and visible.
3. **Make OIDC real** ([#15](https://github.com/himanshu-kr-jha/teamup.sh/issues/15))
   — replace the mock `findOrCreateUser` with genuine provisioning, and read
   provider config from `org_configs` rather than only the environment
   ([#31](https://github.com/himanshu-kr-jha/teamup.sh/issues/31)). Larger, and
   the single biggest gap between what TeamUp claims and what it does.
4. **Populate correlation IDs** ([#21](https://github.com/himanshu-kr-jha/teamup.sh/issues/21))
   — the helpers exist and nothing calls them.
5. **Decide the multipart question** ([#26](https://github.com/himanshu-kr-jha/teamup.sh/issues/26))
   — implement it, or remove the endpoints and document the single-PUT limit.
   The current state is the worst of both, because a client can reasonably
   assume resumability that is not there.

The full list is on
[GitHub](https://github.com/himanshu-kr-jha/teamup.sh/issues); anything
labelled `ready-for-agent` is scoped enough to pick up unattended, and
`ready-for-human` needs a product decision first.

Older issues also live as Markdown under `.scratch/<feature>/`; see
[`docs/agents/issue-tracker.md`](agents/issue-tracker.md).
