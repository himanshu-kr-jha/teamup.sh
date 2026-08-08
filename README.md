# TeamUp - Self-hosted Collaboration Platform

A self-hosted, Teams-like collaboration platform with real-time chat, multi-party video calls, task management, and more.

## Features

- **Real-time Chat**: Channels, DMs, reactions, threads, search
- **Video Calls**: LiveKit-powered multi-party video/audio calls with screen sharing
- **Task Management**: Jira-like tasks with sprints, labels, comments, attachments
- **RBAC**: Role-based access control with custom permissions
- **PWA**: Installable web app with offline support
- **Notifications**: Web Push (VAPID) + in-app notification center
- **File Storage**: MinIO (S3-compatible) for attachments and recordings
- **Auth**: OIDC (Google, Entra ID, custom) with PKCE, JWT + refresh token rotation
- **Observability**: Structured logging, Prometheus metrics, health checks

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 20+ & pnpm
- PostgreSQL 16+
- Redis 7+
- MinIO (or S3-compatible)
- LiveKit server

### Development

```bash
# 1. Start dependencies
docker compose up -d postgres redis minio livekit

# 2. Run migrations
go run ./cmd/teamup migrate up

# 3. Build frontend
cd web && pnpm install && pnpm run build

# 4. Run server
go run ./cmd/teamup server
```

### Production (Docker)

```bash
docker compose -f docker-compose.prod.yml up -d
```

### Production (Helm)

```bash
helm install teamup ./helm/teamup -n teamup --create-namespace
```

## Configuration

All configuration via environment variables or config file:

```bash
# Required
TEAMUP_DATABASE_URL=postgres://user:pass@localhost:5432/teamup?sslmode=disable
TEAMUP_REDIS_URL=redis://localhost:6379
TEAMUP_MINIO_ENDPOINT=minio:9000
TEAMUP_MINIO_ACCESS_KEY=minioadmin
TEAMUP_MINIO_SECRET_KEY=minioadmin
TEAMUP_MINIO_BUCKET=teamup-files
TEAMUP_LIVEKIT_URL=http://livekit:7880
TEAMUP_LIVEKIT_API_KEY=your-key
TEAMUP_LIVEKIT_API_SECRET=your-secret

# Optional
TEAMUP_PORT=8080
TEAMUP_LOG_LEVEL=info
TEAMUP_ENV=production
```

## Project Structure

```
teamup/
├── cmd/teamup/           # Main entry point (server, migrate, wizard, version)
├── internal/             # Private application code
│   ├── auth/             # OIDC, JWT, sessions
│   ├── chat/             # Chat, messages, channels
│   ├── calls/            # LiveKit integration
│   ├── files/            # MinIO file storage
│   ├── notifications/    # Web Push, in-app notifications
│   ├── presence/         # User presence
│   ├── tasks/            # Task management
│   ├── org/              # Organizations, users, RBAC
│   ├── websocket/        # Unified WebSocket hub
│   ├── wizard/           # Setup wizard
│   └── observability/    # Logging, metrics, health
├── pkg/                  # Public packages
│   ├── protocol/         # WebSocket message types
│   └── errors/           # Error types
├── web/                  # React + Vite + TypeScript frontend
├── migrations/           # SQL migrations (golang-migrate)
├── helm/teamup/          # Helm chart
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── Makefile
```

## API Endpoints

### Auth
- `GET /auth/login/:provider` - Initiate OIDC login
- `GET /auth/callback/:provider` - OIDC callback
- `POST /auth/refresh` - Refresh access token
- `POST /auth/logout` - Logout (RP-initiated + back-channel)

### Channels
- `GET /api/channels` - List channels
- `POST /api/channels` - Create channel
- `GET /api/channels/:id` - Get channel
- `PATCH /api/channels/:id` - Update channel
- `DELETE /api/channels/:id` - Archive channel
- `POST /api/channels/:id/members` - Add member
- `DELETE /api/channels/:id/members/:userId` - Remove member

### Messages
- `GET /api/channels/:id/messages` - List messages (with pagination, search)
- `POST /api/channels/:id/messages` - Send message
- `PATCH /api/messages/:id` - Edit message
- `DELETE /api/messages/:id` - Delete message
- `POST /api/messages/:id/reactions` - Add reaction
- `DELETE /api/messages/:id/reactions/:emoji` - Remove reaction

### Tasks
- `GET /api/tasks` - List tasks (with filters)
- `POST /api/tasks` - Create task
- `GET /api/tasks/:id` - Get task
- `PATCH /api/tasks/:id` - Update task
- `DELETE /api/tasks/:id` - Delete task
- `POST /api/tasks/:id/comments` - Add comment
- `POST /api/tasks/:id/attachments` - Add attachment

### Calls
- `POST /api/calls` - Create/schedule call
- `GET /api/calls/:id/token` - Get LiveKit token
- `POST /api/calls/:id/record` - Start recording
- `GET /api/calls` - List calls

### Files
- `POST /api/files/upload-url` - Get presigned upload URL
- `POST /api/files/multipart/initiate` - Initiate multipart upload
- `POST /api/files/multipart/complete` - Complete multipart upload
- `GET /api/files/download-url` - Get presigned download URL

### Notifications
- `GET /api/notifications` - List notifications
- `PATCH /api/notifications/:id/read` - Mark as read
- `PATCH /api/notifications/read-all` - Mark all as read

### Push Subscriptions
- `POST /api/notifications/subscribe` - Subscribe to push
- `DELETE /api/notifications/subscribe` - Unsubscribe

### Search
- `GET /api/search/messages` - Full-text search messages

### Setup Wizard
- `GET /api/wizard/status` - Check setup status
- `POST /api/wizard/step1` - Organization
- `POST /api/wizard/step2` - Admin user
- `POST /api/wizard/step3` - OIDC config
- `POST /api/wizard/step4` - Services test
- `POST /api/wizard/step5` - Complete

### Health & Metrics
- `GET /healthz/live` - Liveness probe
- `GET /healthz/ready` - Readiness probe
- `GET /metrics` - Prometheus metrics

## WebSocket Protocol

Connect: `wss://host/ws?token=<access_token>`

Message envelope:
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
- `conn.init`, `conn.ack`, `conn.ping`, `conn.pong`, `conn.close`
- `chat.send`, `chat.sent`, `chat.message`, `chat.edit`, `chat.delete`, `chat.typing`, `chat.read`
- `presence.update`, `presence.changed`, `presence.sync`
- `call.invite`, `call.invited`, `call.accept`, `call.decline`, `call.ended`, `call.signaling`, `call.signaling.relay`
- `notification.new`, `notification.read`

## License

MIT
