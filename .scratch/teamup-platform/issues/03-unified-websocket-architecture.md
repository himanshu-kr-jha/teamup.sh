## Question

Design the unified WebSocket architecture: single persistent connection per client carrying chat messages, presence updates (online/away/busy/in-call), and call signaling (offer/answer/ICE candidates). Define message protocol (JSON schema), connection lifecycle, reconnection strategy, backpressure handling, and how the Go backend routes call signaling to LiveKit vs. handling chat/presence locally.

Status: claimed

## Answer

**Decision: Unified WebSocket architecture defined below.**

### 1. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client (React PWA)                       │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │ Chat Store  │  │ Presence    │  │ Call SDK    │              │
│  │ (TanStack)  │  │ (Zustand)   │  │ (LiveKit)   │              │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘              │
│         └────────────────┼────────────────┘                       │
│                          ▼                                        │
│              ┌─────────────────────┐                              │
│              │ Unified WS Client   │                              │
│              │ - Auto-reconnect    │                              │
│              │ - Message queue     │                              │
│              │ - Typed handlers    │                              │
│              └──────────┬──────────┘                              │
└─────────────────────────┼────────────────────────────────────────┘
                          │ wss://api.teamup.local/ws
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Go Backend (Single Binary)                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              WebSocket Hub (internal/websocket)         │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐     │   │
│  │  │ Connection  │  │ Message     │  │ Subscription│     │   │
│  │  │ Registry    │  │ Router      │  │ Manager     │     │   │
│  │  └─────────────┘  └──────┬──────┘  └─────────────┘     │   │
│  └─────────────────────────┼───────────────────────────────┘   │
│         ┌────────────────┼────────────────┐                    │
│         ▼                ▼                ▼                    │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐            │
│  │ Chat        │  │ Presence    │  │ Calls       │            │
│  │ Service     │  │ Service     │  │ Service     │            │
│  │ (local)     │  │ (local)     │  │ (LiveKit    │            │
│  │             │  │             │  │  relay)     │            │
│  └─────────────┘  └─────────────┘  └─────────────┘            │
└─────────────────────────────────────────────────────────────────┘
```

**Key principle:** Single WebSocket connection per client. The Go backend's `MessageRouter` inspects `message.type` and dispatches to the appropriate service. Call signaling is relayed to LiveKit via its HTTP API; chat/presence handled locally.

---

### 2. Message Protocol (JSON Schema)

All messages share a common envelope:

```json
{
  "id": "uuid-v4",           // Client-generated for request-response correlation
  "type": "chat.send",       // Namespaced: <domain>.<action>
  "payload": { ... },        // Domain-specific
  "timestamp": "2024-01-15T10:30:00Z",  // Server adds on receipt
  "version": 1               // Protocol version
}
```

#### Message Types

| Domain | Type | Direction | Payload |
|--------|------|-----------|---------|
| **Connection** | `conn.init` | C→S | `{ token: string, clientInfo: { platform, version } }` |
| | `conn.ack` | S→C | `{ connectionId: string, serverTime: string, features: string[] }` |
| | `conn.ping` | C→S / S→C | `{}` |
| | `conn.pong` | S→C / C→S | `{ originalTimestamp: string }` |
| | `conn.close` | Both | `{ reason: string, code: number }` |
| **Auth** | `auth.refresh` | C→S | `{ refreshToken: string }` |
| | `auth.refreshed` | S→C | `{ accessToken: string, expiresIn: number }` |
| **Chat** | `chat.send` | C→S | `{ channelId: string, content: string, replyTo?: string, attachments?: Attachment[] }` |
| | `chat.sent` | S→C | `{ messageId: string, channelId: string, timestamp: string }` |
| | `chat.message` | S→C (broadcast) | `{ message: Message }` |
| | `chat.edit` | C→S | `{ messageId: string, content: string }` |
| | `chat.delete` | C→S | `{ messageId: string }` |
| | `chat.reaction` | C→S | `{ messageId: string, emoji: string, action: "add"|"remove" }` |
| | `chat.typing` | C→S | `{ channelId: string, isTyping: boolean }` |
| | `chat.read` | C→S | `{ channelId: string, messageId: string }` |
| **Presence** | `presence.update` | C→S | `{ status: "online"|"away"|"busy"|"in_call"|"offline", customStatus?: string }` |
| | `presence.changed` | S→C (broadcast) | `{ userId: string, status: string, customStatus?: string, lastSeen?: string }` |
| | `presence.sync` | S→C | `{ users: UserPresence[] }` |
| **Calls** | `call.invite` | C→S | `{ roomName: string, invitees: string[], callType: "video"|"audio" }` |
| | `call.invited` | S→C | `{ roomName: string, callerId: string, callType: string, token: string }` |
| | `call.accept` | C→S | `{ roomName: string }` |
| | `call.decline` | C→S | `{ roomName: string, reason?: string }` |
| | `call.ended` | S→C | `{ roomName: string, duration: number }` |
| | `call.signaling` | C→S | `{ roomName: string, targetParticipant: string, payload: SignalingPayload }` |
| | `call.signaling.relay` | S→C | `{ roomName: string, fromParticipant: string, payload: SignalingPayload }` |

#### Signaling Payload (for `call.signaling`)
```json
{
  "type": "offer|answer|ice-candidate|renegotiate",
  "sdp": "v=0\\r\\no=- ...",  // For offer/answer
  "candidate": "candidate:1 1 UDP ...",  // For ice-candidate
  "sdpMid": "0",
  "sdpMLineIndex": 0
}
```

---

### 3. Connection Lifecycle

```
┌──────────┐     conn.init      ┌──────────┐     conn.ack       ┌──────────┐
│  Client  │ ─────────────────► │  Server  │ ─────────────────► │  Client  │
└──────────┘   (with JWT)        └──────────┘  (connectionId)    └──────────┘
     │                              │                              │
     │         ping/pong (30s)      │                              │
     │ ◄──────────────────────────► │                              │
     │                              │                              │
     │         chat.send            │                              │
     │ ──────────────────────────►  │                              │
     │                              │   chat.sent (ack)            │
     │ ◄──────────────────────────►  │                              │
     │                              │   chat.message (broadcast)   │
     │ ◄────────────────────────────│                              │
     │                              │                              │
     │         conn.close           │                              │
     │ ──────────────────────────►  │                              │
     │                              │                              │
```

**States:** `Connecting` → `Authenticated` → `Ready` → `Closing` → `Closed`

**Authentication:** JWT in `conn.init` payload (or cookie). Server validates, extracts `userId`, `orgId`, `roles`. Rejects with `conn.close` (code 4001) if invalid.

**Connection ID:** Server-generated UUID, returned in `conn.ack`. Used for:
- Targeted broadcasts (DM, call invites)
- Debugging/logging
- Admin kick (`POST /admin/connections/{id}/kick`)

---

### 4. Reconnection Strategy

#### Client-Side (React Hook)
```typescript
// hooks/useUnifiedWebSocket.ts
const RECONNECT_DELAYS = [1000, 2000, 5000, 10000, 30000]; // Exponential backoff
const MAX_RECONNECT_ATTEMPTS = 5;

export function useUnifiedWebSocket() {
    const [state, setState] = useState<WSState>('connecting');
    const wsRef = useRef<WebSocket>();
    const attemptRef = useRef(0);
    const messageQueueRef = useRef<OutgoingMessage[]>([]);

    const connect = useCallback(() => {
        const ws = new WebSocket(getWSUrl());
        wsRef.current = ws;

        ws.onopen = () => {
            setState('authenticating');
            ws.send(JSON.stringify({
                id: crypto.randomUUID(),
                type: 'conn.init',
                payload: { token: getAccessToken(), clientInfo: getClientInfo() }
            }));
        };

        ws.onmessage = (event) => {
            const msg = JSON.parse(event.data);
            handleIncoming(msg);
        };

        ws.onclose = (e) => {
            setState('disconnected');
            if (!e.wasClean && attemptRef.current < MAX_RECONNECT_ATTEMPTS) {
                const delay = RECONNECT_DELAYS[Math.min(attemptRef.current, RECONNECT_DELAYS.length - 1)];
                attemptRef.current++;
                setTimeout(connect, delay);
            }
        };
    }, []);

    const send = useCallback((msg: OutgoingMessage) => {
        if (wsRef.current?.readyState === WebSocket.OPEN) {
            wsRef.current.send(JSON.stringify(msg));
        } else {
            messageQueueRef.current.push(msg); // Flush on reconnect
        }
    }, []);

    // Flush queue on reconnect (after conn.ack)
    const handleIncoming = (msg) => {
        if (msg.type === 'conn.ack') {
            messageQueueRef.current.forEach(m => send(m));
            messageQueueRef.current = [];
            setState('ready');
            attemptRef.current = 0;
        }
        // ... route to domain handlers
    };

    return { state, send, connect };
}
```

#### Server-Side Session Resumption
- On reconnect, client sends same `conn.init` with valid JWT
- Server issues new `connectionId`, returns `conn.ack`
- Client re-subscribes to channels (implicit via presence)
- **No message replay** — client fetches missed messages via REST (`GET /api/channels/{id}/messages?since={lastMessageId}`)

---

### 5. Backpressure Handling

| Layer | Mechanism |
|-------|-----------|
| **Network** | TCP flow control (kernel) |
| **WebSocket** | `nhooyr/websocket` `SetWriteLimit(16MB)`, `SetReadLimit(16MB)` |
| **Application** | Per-connection bounded channel (`chan OutgoingMessage, 256`) |
| **Broadcast** | Fan-out via subscription manager — slow clients disconnected after 10s buffer |

**Go Implementation:**
```go
// internal/websocket/connection.go
type Connection struct {
    id          string
    userID      string
    orgID       string
    ws          *websocket.Conn
    sendCh      chan *OutgoingMessage  // Buffered: 256
    subscriptions map[string]bool      // channelIDs
    closeOnce   sync.Once
}

func (c *Connection) WriteLoop(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second) // Ping interval
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case msg, ok := <-c.sendCh:
            if !ok { return }
            if err := c.writeJSON(ctx, msg); err != nil {
                log.Error().Err(err).Str("conn", c.id).Msg("write failed")
                return
            }
        case <-ticker.C:
            if err := c.writeJSON(ctx, &OutgoingMessage{Type: "conn.ping", Payload: map[string]any{}}); err != nil {
                return
            }
        }
    }
}

func (c *Connection) Enqueue(msg *OutgoingMessage) bool {
    select {
    case c.sendCh <- msg:
        return true
    default:
        // Buffer full — client is slow
        metrics.WebSocketBackpressureDrops.Inc()
        return false
    }
}
```

**Slow client policy:** If `Enqueue` fails 3× in 10s → `conn.close` (code 4002 "backpressure").

---

### 6. Call Signaling Routing

**Critical distinction:** Call signaling (SDP, ICE) goes to LiveKit, NOT peer-to-peer via our WS.

```
Client A                          Go Backend                          LiveKit
   │                                 │                                   │
   ├── call.invite ────────────────► │                                   │
   │                                 ├── CreateRoom(roomName) ────────► │
   │                                 │◄──────── Room created             │
   │                                 ├── GenerateToken(A) ────────────► │
   │                                 │◄──────── Token                    │
   │◄──── call.invited (token) ──────│                                   │
   │                                 │                                   │
   ├── call.signaling (offer) ─────► │                                   │
   │                                 │   (relay via LiveKit API)        │
   │                                 ├── SendSignal(targetB, offer) ──► │
   │                                 │                                   │
   │                                 │◄──────── Signal to B             │
   │                                 │                                   │
   │◄──── call.signaling.relay ──────│  (answer/ICE from B)            │
   │                                 │                                   │
```

**Why not P2P via our WS?**
- LiveKit handles ICE candidate gathering, TURN, NAT traversal
- SFU topology requires server-side routing anyway
- Our WS is for **invitation/coordination**, not media signaling

**Go Backend Relay:**
```go
// internal/calls/signaling.go
func (s *Service) HandleSignaling(ctx context.Context, conn *ws.Connection, msg *IncomingMessage) {
    var payload SignalingPayload
    if err := json.Unmarshal(msg.Payload, &payload); err != nil { return }

    // Validate: user is in this room
    if !s.isParticipant(ctx, msg.Payload.RoomName, conn.userID) {
        return
    }

    // Relay to LiveKit (which forwards to target participant)
    err := s.lkClient.SendSignal(ctx, livekit.SignalRequest{
        Destination: payload.TargetParticipant,
        Signal: &livekit.SignalRequest_Offer{ Offer: payload.SDP },
        // ... answer, ice, etc.
    })
}
```

**Frontend:** Uses LiveKit SDK for actual WebRTC connection. Our WS only carries:
- `call.invite` / `call.invited` (coordination)
- `call.accept` / `call.decline` (intent)
- `call.signaling` → backend → LiveKit (relay)

---

### 7. Subscription Model (Chat + Presence)

**Channels:** User subscribes to channels they're a member of. On `conn.ack`, server sends `presence.sync` with all online users in shared channels.

**Channel membership changes** (added/removed) → `chat.membership.changed` broadcast.

**Presence:** Broadcast to all connections sharing at least one channel with the user.

```go
// internal/websocket/hub.go
func (h *Hub) BroadcastToChannel(channelID string, msg *OutgoingMessage, excludeUserID string) {
    h.mu.RLock()
    conns := h.channelSubs[channelID]  // map[userID]*Connection
    h.mu.RUnlock()

    for userID, conn := range conns {
        if userID == excludeUserID { continue }
        conn.Enqueue(msg)
    }
}
```

---

### 8. Security

| Threat | Mitigation |
|--------|------------|
| **Token theft** | Short-lived access tokens (15m), refresh via `auth.refresh` (HttpOnly cookie) |
| **WS hijacking** | `conn.init` requires valid JWT; `Origin` header validation; `Secure` cookies |
| **Message injection** | Server validates `channelId` membership on every `chat.send` |
| **DoS via large messages** | `SetReadLimit(1MB)`; reject oversized payloads |
| **Connection exhaustion** | Per-IP connection limit (100); per-user limit (10 devices) |

---

### 9. Open Questions (for later)

- Message encryption at rest (pgcrypto) / in transit (TLS only v1)
- WebSocket compression (permessage-deflate) — `nhooyr/websocket` supports it
- Horizontal scaling: Redis pub/sub for cross-node broadcasts (when >1 Go replica)
- Offline message queue persistence (Redis streams) — v2

Status: resolved
