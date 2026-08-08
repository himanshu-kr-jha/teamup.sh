## Destination

A complete spec with locked architectural decisions for a self-hosted, Teams-like collaboration platform: real-time chat, multi-party video calls (LiveKit), RBAC hierarchy, Jira-like task management, PWA mobile access — deployable as single Go binary, Docker Compose, or Helm chart; single-tenant per startup on their own infrastructure.

## Notes

- **Domain**: Startup collaboration platform (chat, calls, tasks, hierarchy, availability)
- **Skills to consult**: `/grilling`, `/domain-modeling`, `/codebase-design`, `/research` (for LiveKit, Go WebRTC, PWA patterns)
- **Standing preferences**: Self-hosted, single-tenant, lightning-fast, single binary deploy, PWA-first mobile, no external SaaS deps in v1
- **Auth**: OIDC (Google Workspace, Entra ID, custom)
- **Video**: LiveKit SFU (self-hosted)
- **Real-time**: Unified WebSocket (chat + presence + call signaling)
- **Storage**: MinIO (S3-compatible)
- **Search**: Postgres FTS v1, Meilisearch later
- **Notifications**: In-app + Web Push (VAPID)
- **Offline**: Read-only cache + optimistic writes (IndexedDB + SW)
- **CI/CD**: GitHub Actions → binary + Docker + Helm → GitHub Releases
- **Onboarding**: Web setup wizard
- **Observability**: JSON stdout + /healthz + Prometheus metrics

## Decisions so far

<!-- populated as tickets resolve -->

- [Backend language: Go](.scratch/teamup-platform/issues/01-backend-language-go.md) — Go confirmed for single-binary deploy, goroutine concurrency, LiveKit Go SDK, nhooyr/websocket, pion/webrtc; project structure and key libraries documented
- [LiveKit integration](.scratch/teamup-platform/issues/02-livekit-integration.md) — Self-hosted LiveKit (Docker Compose/Helm + Redis), Go token generation/validation, participant lifecycle, recording to MinIO, React SDK (@livekit/components-react), SIP deferred, scaling to 1000 concurrent users on 2 nodes
- [Unified WebSocket architecture](.scratch/teamup-platform/issues/03-unified-websocket-architecture.md) — Single WS connection per client; JSON envelope protocol (conn/chat/presence/call namespaces); nhooyr/websocket with backpressure (256-msg buffer); exponential backoff reconnection; call signaling relayed to LiveKit via Go backend; chat/presence handled locally; channel-based subscription model
- [MinIO file storage](.scratch/teamup-platform/issues/04-minio-file-storage.md) — Org-scoped bucket (teamup-files-{orgId}); direct client upload via presigned PUT/multipart URLs; 50MB threshold for multipart; retention policies per category (chat 1yr, tasks 3yr, recordings 30d); thumbnail generation async; virus scanning deferred; DB schema for attachments
- [Postgres FTS search](.scratch/teamup-platform/issues/05-postgres-fts-search.md) — Generated tsvector column (STORED) with GIN index; websearch_to_tsquery for Google-style queries; ts_rank_cd(32) ranking; pg_trgm for fuzzy/prefix; ts_headline highlighting; filtered search API with pagination; <200ms p95 target for 10M messages
- [Web Push notifications](.scratch/teamup-platform/issues/06-web-push-notifications.md) — VAPID keys generated at setup; per-device push subscriptions stored in DB; webpush-go for encrypted payload delivery; urgency/TTL per notification type; SW handles pushclick/focus; in-app notification center with real-time WS updates; contextual permission prompt (not on load)
- [PWA offline strategy](.scratch/teamup-platform/issues/07-pwa-offline-strategy.md) — Workbox: CacheFirst static, StaleWhileRevalidate API GET, NetworkOnly+BackgroundSync mutations; IndexedDB (channels, messages, tasks, users, outbox); optimistic writes with temp IDs; last-write-wins conflict resolution; offline banner with sync status; 500 msg/channel cache limit
- [CI/CD pipeline](.scratch/teamup-platform/issues/08-cicd-github-actions.md) — GitHub Actions: lint → unit/integration/e2e tests → multi-arch Go binary (amd64/arm64) with embedded Vite frontend → distroless Docker → GHCR → Helm chart → GitHub Release (semver tags); Makefile targets; conventional commits for changelog
- [Setup wizard](.scratch/teamup-platform/issues/09-setup-wizard.md) — 6-step web wizard (welcome → org → admin → OIDC → services test → complete); first-run detection via empty DB; env var auto-setup for CI/CD; idempotent finalization (org, admin, RBAC, configs); connection tests for MinIO/LiveKit/SMTP; VAPID keys generated; encrypted session cookie
- [Observability](.scratch/teamup-platform/issues/10-observability.md) — zerolog JSON logging with correlation IDs; /healthz (liveness) + /readyz (readiness); Prometheus RED metrics (HTTP, WS, DB, LiveKit) + custom business metrics (active users, calls, messages, tasks); Grafana dashboards via ServiceMonitor; alerting rules; log sampling (1%) for high-traffic paths; OpenTelemetry tracing deferred to v2
- [Database schema](.scratch/teamup-platform/issues/11-database-schema.md) — Complete PostgreSQL schema: orgs, users, RBAC roles/permissions, teams hierarchy, channels (DM/group/public), messages (FTS), Jira-like tasks (status/priority/sprints/labels/comments/attachments), calls (LiveKit mapping), presence, push subscriptions, attachments, notifications, org configs, audit logs; golang-migrate for versioned migrations; soft deletes; org isolation via FK
- [OIDC authentication](.scratch/teamup-platform/issues/12-oidc-auth.md) — Provider abstraction (Google, Entra ID, custom OIDC); PKCE for PWA; JWT access tokens (15m) + refresh token rotation (30d) with HttpOnly Secure cookies; role mapping via provider groups; account linking; RP-initiated + back-channel logout; RS256 signing
- [Frontend architecture](.scratch/teamup-platform/issues/13-frontend-architecture.md) — Vite + React 18 + TypeScript; TanStack Query (server state) + Zustand (client state); shadcn/ui (Radix + Tailwind); React Router v6; LiveKit React SDK; typed WS client with auto-reconnect/queue; Workbox PWA (StaleWhileRevalidate GET, BackgroundSync mutations); idb for offline cache; Go embed for single-binary deploy

## Not yet specified

- Mobile native apps (React Native / Flutter / Capacitor) — PWA v1, native later
- AI features (meeting minutes, task extraction, smart summaries) — explicitly deferred
- Full calendar UI (recurrence, timezone, resource booking) — minimal scheduling v1, integrate Cal.com later
- Advanced task views (boards, timelines, sprints, dependencies, time tracking) — Jira-like core v1
- Federation / multi-org / guest access — single-tenant v1
- Plugin / extension system — not v1
- Email notifications — v1 is in-app + Web Push only
- Call recording storage / retention policy — LiveKit records, policy TBD
- End-to-end encryption for chat/calls — not v1
- Accessibility (WCAG 2.1 AA) — baseline only v1
- Internationalization (i18n) — English only v1
- Data export / GDPR tooling — not v1
- Audit logs for compliance — not v1

## Out of scope

- SaaS multi-tenant hosting (we ship software, not service)
- Microsoft Teams embed / integration (standalone, Teams-like UX)
- Custom WebRTC implementation from scratch (LiveKit handles SFU)
- Managed video services (Daily.co, Twilio, Agora) — violates self-hosted requirement
- Email notifications v1 (Web Push + in-app only)
- Native mobile apps v1 (PWA only)
- Full calendar replacement (Cal.com integration later)
