## Question

Define the LiveKit integration architecture: self-hosted LiveKit server deployment (Docker Compose / Helm), token generation and validation in Go backend, participant lifecycle (join/leave/mute), recording configuration, SIP integration (if needed), and client-side SDK usage (React + PWA). Include scaling considerations for multi-party calls.

Status: claimed

## Answer

**Decision: LiveKit integration architecture defined below.**

### 1. LiveKit Server Deployment

#### Docker Compose (v1, single-node)
```yaml
# docker-compose.yml (excerpt)
services:
  livekit:
    image: livekit/livekit-server:latest
    ports:
      - "7880:7880"   # HTTP API
      - "7881:7881"   # HTTPS API (if TLS)
      - "7882:7882"   # WebRTC UDP (media)
    environment:
      LIVEKIT_CONFIG: |
        port: 7880
        rtc:
          tcp_port: 7881
          port_range_start: 50000
          port_range_end: 60000
          use_external_ip: true
        keys:
          "api-key-123": "api-secret-456"  # In prod: use file/ENV
        redis:
          address: "redis:6379"  # For multi-node scaling
    depends_on:
      - redis
    networks:
      - teamup-net

  redis:
    image: redis:7-alpine
    networks:
      - teamup-net
```

#### Helm (production, multi-node)
```yaml
# helm/teamup/values.yaml (excerpt)
livekit:
  enabled: true
  replicaCount: 2  # Start with 2 for HA
  image:
    repository: livekit/livekit-server
    tag: latest
  config:
    port: 7880
    rtc:
      tcpPort: 7881
      portRangeStart: 50000
      portRangeEnd: 60000
      useExternalIP: true
    keys:
      "api-key-123": "api-secret-456"
    redis:
      address: "redis-master:6379"
  resources:
    limits:
      cpu: 2000m
      memory: 2Gi
    requests:
      cpu: 500m
      memory: 1Gi
  service:
    type: ClusterIP
    ports:
      - port: 7880
        name: http
      - port: 7881
        name: rtc-tcp
      - port: 7882
        name: rtc-udp
        protocol: UDP
```

**Key ports:**
- `7880` — HTTP API (token validation, room management, webhooks)
- `7881` — TCP for WebRTC (ICE/TCP fallback)
- `50000-60000` — UDP media ports (RTP/RTCP)

**Redis required** for multi-node: participant presence, room state, distributed locking.

---

### 2. Token Generation & Validation (Go Backend)

#### Token Structure (JWT)
```go
// internal/calls/token.go
package calls

import (
    "time"
    "github.com/livekit/server-sdk-go/v2"
)

type TokenClaims struct {
    RoomName   string
    ParticipantName string
    ParticipantIdentity string
    Metadata   string // JSON: userID, orgID, role
    CanPublish bool
    CanSubscribe bool
    CanPublishData bool
}

func (s *Service) GenerateToken(claims TokenClaims) (string, error) {
    at := livekit.NewAccessToken(s.cfg.LiveKitAPIKey, s.cfg.LiveKitAPISecret)
    at.SetVideoGrant(&livekit.VideoGrant{
        RoomJoin:           true,
        Room:               claims.RoomName,
        RoomAdmin:          claims.CanPublish, // Admin can mute/remove
        RoomRecord:         false,             // Controlled server-side
        RoomCreate:         false,
        CanPublish:         claims.CanPublish,
        CanSubscribe:       claims.CanSubscribe,
        CanPublishData:     claims.CanPublishData,
    })
    at.SetIdentity(claims.ParticipantIdentity)
    at.SetName(claims.ParticipantName)
    at.SetMetadata(claims.Metadata)
    at.SetValidFor(24 * time.Hour) // Session duration
    return at.ToJWT()
}
```

#### Token Validation (Webhook)
LiveKit sends webhooks to `/api/webhooks/livekit` — validate with API secret:
```go
// internal/calls/webhook.go
func (s *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
    event, err := livekit.ReceiveWebhookEvent(r, s.cfg.LiveKitAPISecret)
    if err != nil { /* 401 */ }

    switch event.Event {
    case "room_started", "room_finished":
        s.handleRoomEvent(event)
    case "participant_joined", "participant_left":
        s.handleParticipantEvent(event)
    case "track_published", "track_unpublished":
        s.handleTrackEvent(event)
    case "recording_started", "recording_finished":
        s.handleRecordingEvent(event)
    }
}
```

---

### 3. Participant Lifecycle

| Event | Backend Action | Frontend (React) |
|-------|----------------|------------------|
| **Join** | Generate token → return to client | `room.connect(url, token)` |
| **Leave** | Webhook `participant_left` → update presence, cleanup | `room.disconnect()` |
| **Mute/Unmute (self)** | Track unpublished/published → broadcast via WS | `localParticipant.setMicrophoneEnabled(false)` |
| **Mute/Unmute (admin)** | Server calls LiveKit API `MutePublishedTrack` | Receives `RemoteTrack.muted` event |
| **Remove participant** | Server calls LiveKit API `RemoveParticipant` | Receives `disconnected` with reason |
| **Reconnect** | New token (short TTL) → `room.connect()` | Auto-reconnect via SDK |

**Admin controls** (CEO/Manager): `RoomAdmin` grant → can mute, remove, lock room.

---

### 4. Recording Configuration

#### Server-side recording (recommended for compliance)
```go
// internal/calls/recording.go
func (s *Service) StartRecording(roomName, outputPath string) error {
    req := livekit.RoomRecordingRequest{
        RoomName: roomName,
        Output: &livekit.RecordingOutput{
            FileType: livekit.RecordingOutput_MP4,
            Filepath: outputPath, // MinIO path
        },
        Preset: livekit.RecordingPreset_H264_720P_30,
    }
    return s.lkClient.StartRecording(ctx, req)
}
```

#### Egress to MinIO (S3-compatible)
```yaml
# livekit.yaml (egress config)
egress:
  s3:
    access_key: "minioadmin"
    secret_key: "minioadmin"
    region: "us-east-1"
    endpoint: "http://minio:9000"
    bucket: "teamup-recordings"
    force_path_style: true
```

**Recording modes:**
- `RoomComposite` — single MP4, server-side composition (default)
- `TrackComposite` — per-track files (for post-processing)
- `Participant` — individual participant recordings

**Retention policy:** Configurable (default 30 days), cleanup job in Go backend.

---

### 5. SIP Integration (Deferred to v2)

**Not in v1 scope.** LiveKit SIP supports:
- Inbound: PSTN → LiveKit room (phone dial-in)
- Outbound: LiveKit room → PSTN (call phone numbers)
- Requires SIP trunk (Twilio, Telnyx, Asterisk)

Add as fog ticket: "SIP trunk integration for phone dial-in/out."

---

### 6. Client-Side SDK (React + PWA)

#### Installation
```bash
npm install @livekit/components-react @livekit/components-styles livekit-client
```

#### Core Hook (simplified)
```tsx
// hooks/useLiveKitRoom.ts
import { useRoom, RoomEvent } from '@livekit/components-react';

export function useLiveKitRoom(roomName: string, token: string) {
    const room = useRoom({
        url: import.meta.env.VITE_LIVEKIT_URL,
        token,
        connectOptions: { autoSubscribe: true },
    });

    const join = useCallback(() => room.connect(), [room]);
    const leave = useCallback(() => room.disconnect(), [room]);
    const toggleMic = useCallback(() =>
        room.localParticipant?.setMicrophoneEnabled(!room.localParticipant?.isMicrophoneEnabled()),
        [room]
    );

    return { room, join, leave, toggleMic,
        participants: room.remoteParticipants,
        isConnected: room.connectionState === 'connected' };
}
```

#### UI Components (using `@livekit/components-react`)
```tsx
// components/CallView.tsx
import {
    LiveKitRoom,
    VideoConference,
    ParticipantTile,
    Toolbar,
    useParticipants
} from '@livekit/components-react';

export function CallView({ roomName, token }) {
    return (
        <LiveKitRoom url={LIVEKIT_URL} token={token} connect={true}>
            <VideoConference>
                <ParticipantTile participant={localParticipant} />
                {remoteParticipants.map(p => (
                    <ParticipantTile key={p.identity} participant={p} />
                ))}
            </VideoConference>
            <Toolbar
                devices={true}
                microphone={true}
                camera={true}
                screenShare={true}
                leave={true}
            />
        </LiveKitRoom>
    );
}
```

**PWA considerations:**
- Service Worker must **not** cache LiveKit WebSocket/HTTP traffic
- `navigator.mediaDevices.getUserMedia()` requires HTTPS (or localhost)
- Background: `documentPictureInPicture` for PiP on mobile

---

### 7. Scaling Considerations

| Dimension | Strategy |
|-----------|----------|
| **Participants per room** | LiveKit SFU handles 100+ per room; recommend ≤50 for good UX |
| **Concurrent rooms** | Horizontal: add LiveKit nodes + Redis; each node ~500-1000 participants |
| **Media throughput** | UDP port range (50k-60k) per node; ensure firewall/security groups allow |
| **Bandwidth** | SFU forwards ~2-4 Mbps per participant (720p); plan egress |
| **Recording** | Egress to MinIO/S3 runs on separate workers; doesn't block SFU |
| **Geo-distribution** | LiveKit Cloud (managed) or self-hosted regions with TURN; v2 |

**v1 capacity target:** 50 concurrent rooms × 20 participants = 1000 simultaneous users on 2-node LiveKit + Redis.

---

### 8. Integration Points with TeamUp Backend

| TeamUp Feature | LiveKit Interaction |
|----------------|---------------------|
| **Scheduled meetings** | Create room on schedule → tokens emailed/pushed |
| **Ad-hoc calls** | Create room on-demand → invite via WS → tokens generated |
| **Chat during call** | Data channel (`canPublishData`) → relayed to chat service |
| **Screen share** | Separate track → `ParticipantTile` renders automatically |
| **Recording** | Host clicks "Record" → backend calls `StartRecording` → stored in MinIO |
| **Presence** | `participant_joined/left` webhooks → broadcast via unified WS |

---

### 9. Open Questions (for later)

- TURN server config (coturn) for restrictive NATs — add if needed
- Simulcast/SVC settings for adaptive bitrate — LiveKit defaults are good
- E2E encryption (E2EE) — not v1, LiveKit supports via `E2EEManager`
- LiveKit Cloud vs self-hosted cost analysis — self-hosted confirmed

Status: resolved
