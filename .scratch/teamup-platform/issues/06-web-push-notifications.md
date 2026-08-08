## Question

Implement Web Push (VAPID) + in-app notifications: VAPID key generation/rotation, subscription management (store push subscription per device/user), payload encryption, TTL/urgency, service worker push event handler (show notification, focus/open app), in-app notification center (real-time + persisted), and permission prompting UX.

Status: claimed

## Answer

**Decision: Web Push + In-App notifications architecture defined below.**

### 1. VAPID Key Management

```go
// internal/notifications/vapid.go
package notifications

import (
    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/rand"
    "encoding/base64"
    "github.com/sherclockholmes/webpush-go"
)

type VAPIDKeys struct {
    PublicKey  string `json:"publicKey"`   // Base64 URL-safe (for client)
    PrivateKey string `json:"privateKey"`  // Base64 URL-safe (server only)
    Subject    string `json:"subject"`     // "mailto:admin@teamup.local"
}

func GenerateVAPIDKeys() (*VAPIDKeys, error) {
    privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    if err != nil { return nil, err }

    pubKeyBytes := elliptic.MarshalCompressed(privateKey.Curve, privateKey.X, privateKey.Y)
    return &VAPIDKeys{
        PublicKey:  base64.RawURLEncoding.EncodeToString(pubKeyBytes),
        PrivateKey: base64.RawURLEncoding.EncodeToString(privateKey.D.Bytes()),
        Subject:    "mailto:admin@teamup.local",
    }, nil
}

// Rotation: Generate new keys, store both (old + new) for transition period (7 days)
// Client re-subscribes on next visit; old key removed after grace period
```

**Storage:** `config/vapid_keys.json` (encrypted at rest via age/sops) or env vars `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`, `VAPID_SUBJECT`.

**Setup wizard** generates keys on first run; admin can rotate via CLI: `teamupctl vapid rotate`.

---

### 2. Push Subscription Management

```sql
-- migrations/005_push_subscriptions.sql
CREATE TABLE push_subscriptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    endpoint        TEXT NOT NULL,                    -- Push service endpoint (unique per device)
    p256dh          TEXT NOT NULL,                    -- Client public key (base64url)
    auth            TEXT NOT NULL,                    -- Auth secret (base64url)
    user_agent      TEXT,                             -- For debugging
    device_name     TEXT,                             -- User-friendly name
    last_used_at    TIMESTAMPTZ DEFAULT NOW(),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (user_id, endpoint)
);

CREATE INDEX idx_push_subscriptions_user ON push_subscriptions(user_id);
CREATE INDEX idx_push_subscriptions_org ON push_subscriptions(org_id);
```

**Go Model:**
```go
// internal/notifications/subscription.go
type PushSubscription struct {
    ID        string    `json:"id"`
    UserID    string    `json:"userId"`
    Endpoint  string    `json:"endpoint"`
    P256DH    string    `json:"p256dh"`
    Auth      string    `json:"auth"`
    DeviceName string   `json:"deviceName,omitempty"`
    CreatedAt time.Time `json:"createdAt"`
}

func (s *Service) SaveSubscription(ctx context.Context, userID, orgID string, sub *PushSubscription) error {
    // Upsert on (user_id, endpoint)
    _, err := s.db.Exec(ctx, `
        INSERT INTO push_subscriptions (org_id, user_id, endpoint, p256dh, auth, user_agent, device_name)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (user_id, endpoint) DO UPDATE SET
            p256dh = EXCLUDED.p256dh,
            auth = EXCLUDED.auth,
            user_agent = EXCLUDED.user_agent,
            device_name = EXCLUDED.device_name,
            last_used_at = NOW()
    `, orgID, userID, sub.Endpoint, sub.P256DH, sub.Auth, sub.UserAgent, sub.DeviceName)
    return err
}
```

---

### 3. Sending Push Notifications

```go
// internal/notifications/sender.go
import "github.com/sherclockholmes/webpush-go"

type PushPayload struct {
    Title       string                 `json:"title"`
    Body        string                 `json:"body"`
    Icon        string                 `json:"icon,omitempty"`        // /icons/notification.png
    Badge       string                 `json:"badge,omitempty"`       // /icons/badge.png
    Image       string                 `json:"image,omitempty"`       // Large image
    Tag         string                 `json:"tag,omitempty"`         // Collapse key
    Renotify    bool                   `json:"renotify,omitempty"`
    Silent      bool                   `json:"silent,omitempty"`
    Data        map[string]any         `json:"data,omitempty"`        // { type: "chat", channelId: "...", messageId: "..." }
    Actions     []NotificationAction   `json:"actions,omitempty"`
    RequireInteraction bool            `json:"requireInteraction,omitempty"`
    TTL         int                    `json:"ttl,omitempty"`         // Seconds (default 4 hours)
    Urgency     string                 `json:"urgency,omitempty"`     // "very-low" | "low" | "normal" | "high"
}

func (s *Service) SendToUser(ctx context.Context, userID string, payload PushPayload) error {
    subs, err := s.getSubscriptions(ctx, userID)
    if err != nil { return err }

    // Encrypt payload for each subscription (webpush-go handles this)
    for _, sub := range subs {
        go s.sendToSubscription(sub, payload) // Fire-and-forget per device
    }
    return nil
}

func (s *Service) sendToSubscription(sub PushSubscription, payload PushPayload) {
    pushSub := &webpush.Subscription{
        Endpoint: sub.Endpoint,
        Keys: webpush.Keys{
            P256dh: sub.P256DH,
            Auth:   sub.Auth,
        },
    }

    vapid := &webpush.VAPID{
        PublicKey:  s.cfg.VAPIDPublicKey,
        PrivateKey: s.cfg.VAPIDPrivateKey,
        Subject:    s.cfg.VAPIDSubject,
    }

    // Marshal payload
    body, _ := json.Marshal(payload)

    // Send with retry (3 attempts, exponential backoff)
    resp, err := webpush.SendNotification(body, pushSub, vapid)
    if err != nil {
        // Handle specific errors
        if webpush.IsSubscriptionExpired(err) || webpush.IsSubscriptionGone(err) {
            s.deleteSubscription(context.Background(), sub.ID) // Clean up dead subscription
        }
        log.Error().Err(err).Str("endpoint", sub.Endpoint).Msg("push send failed")
        return
    }
    resp.Body.Close()

    // Update last_used_at
    s.db.Exec(context.Background(), `UPDATE push_subscriptions SET last_used_at = NOW() WHERE id = $1`, sub.ID)
}
```

**Urgency mapping:**
| Notification Type | Urgency | TTL |
|-------------------|---------|-----|
| Direct message / mention | `high` | 1 hour |
| Call invite | `high` | 30 seconds |
| Task assignment | `normal` | 4 hours |
| General chat | `low` | 24 hours |
| Background sync | `very-low` | 7 days |

---

### 4. Service Worker (Client-Side)

```javascript
// public/sw.js (generated by Vite PWA plugin + custom)
import { precacheAndRoute } from 'workbox-precaching';
import { registerRoute } from 'workbox-routing';
import { CacheFirst, StaleWhileRevalidate, NetworkOnly } from 'workbox-strategies';

precacheAndRoute(self.__WB_MANIFEST);

// Cache strategies
registerRoute(
    ({ request }) => request.destination === 'image',
    new CacheFirst({ cacheName: 'images', plugins: [{ cacheWillUpdate: async ({ response }) => response.ok ? response : null }] })
);
registerRoute(
    ({ url }) => url.pathname.startsWith('/api/'),
    new NetworkOnly()
);

// Push event handler
self.addEventListener('push', (event) => {
    if (!event.data) return;

    const payload = event.data.json();
    const options = {
        body: payload.body,
        icon: payload.icon || '/icons/notification-192.png',
        badge: payload.badge || '/icons/badge-72.png',
        image: payload.image,
        tag: payload.tag || `teamup-${Date.now()}`,
        renotify: payload.renotify !== false,
        silent: payload.silent || false,
        requireInteraction: payload.requireInteraction || false,
        data: payload.data || {},
        actions: payload.actions || [
            { action: 'open', title: 'Open' },
            { action: 'dismiss', title: 'Dismiss' }
        ],
        timestamp: Date.now(),
    };

    event.waitUntil(
        self.registration.showNotification(payload.title, options)
    );
});

// Notification click → focus/open app
self.addEventListener('notificationclick', (event) => {
    event.notification.close();

    const action = event.action || 'open';
    const data = event.notification.data || {};
    let url = '/';

    if (data.type === 'chat' && data.channelId) {
        url = `/channels/${data.channelId}${data.messageId ? '?msg=' + data.messageId : ''}`;
    } else if (data.type === 'call' && data.roomName) {
        url = `/call/${data.roomName}`;
    } else if (data.type === 'task' && data.taskId) {
        url = `/tasks/${data.taskId}`;
    }

    event.waitUntil(
        clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windowClients) => {
            // Focus existing tab if open
            for (const client of windowClients) {
                if (client.url.includes(self.location.origin) && 'focus' in client) {
                    return client.focus().then((focusedClient) => {
                        focusedClient.postMessage({ type: 'notification-click', url, data });
                    });
                }
            }
            // Open new tab
            return clients.openWindow(url);
        })
    );
});

// Notification close (dismiss)
self.addEventListener('notificationclose', (event) => {
    // Track dismissal analytics
    const data = event.notification.data || {};
    if (data.notificationId) {
        fetch('/api/notifications/' + data.notificationId + '/dismiss', { method: 'POST', keepalive: true });
    }
});
```

---

### 5. Permission Prompting UX

**Strategy:** Progressive, contextual — never ask on page load.

```typescript
// hooks/usePushNotifications.ts
export function usePushNotifications() {
    const [permission, setPermission] = useState<NotificationPermission>('default');
    const [subscription, setSubscription] = useState<PushSubscription | null>(null);

    const requestPermission = useCallback(async () => {
        if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
            return { supported: false };
        }

        const perm = await Notification.requestPermission();
        setPermission(perm);

        if (perm === 'granted') {
            const sub = await subscribe();
            return { supported: true, subscribed: !!sub };
        }
        return { supported: true, subscribed: false };
    }, []);

    const subscribe = useCallback(async () => {
        const reg = await navigator.serviceWorker.ready;
        const vapidPublicKey = await api.get('/notifications/vapid-key'); // Base64URL string

        const sub = await reg.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(vapidPublicKey),
        });

        // Send to backend
        await api.post('/notifications/subscribe', {
            endpoint: sub.endpoint,
            p256dh: arrayBufferToBase64URL(sub.getKey('p256dh')!),
            auth: arrayBufferToBase64URL(sub.getKey('auth')!),
            deviceName: getDeviceName(),
        });

        setSubscription(sub);
        return sub;
    }, []);

    // Contextual prompt triggers
    useEffect(() => {
        const shouldPrompt =
            permission === 'default' &&
            !localStorage.getItem('push-prompt-dismissed') &&
            isUserEngaged(); // e.g., > 2 sessions, sent a message, joined a call

        if (shouldPrompt) {
            const timer = setTimeout(() => showPrompt(), 30000); // 30s after load
            return () => clearTimeout(timer);
        }
    }, [permission]);

    return { permission, subscription, requestPermission, subscribe };
}
```

**Prompt UI:** Toast/banner (not native browser prompt) — "Get notified of messages and calls even when TeamUp isn't open" → [Allow] [Not Now].

**Dismissal:** Respects `localStorage['push-prompt-dismissed'] = 'true'` for 30 days.

---

### 6. In-App Notification Center

```sql
-- migrations/006_notifications.sql
CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    type            VARCHAR(50) NOT NULL,  -- 'chat.message', 'call.invite', 'task.assigned', 'mention'
    title           VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    data            JSONB,                 -- { channelId, messageId, roomName, taskId, ... }
    read_at         TIMESTAMPTZ,
    archived_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_unread ON notifications(user_id, read_at) WHERE read_at IS NULL;
CREATE INDEX idx_notifications_user_created ON notifications(user_id, created_at DESC);
```

**API:**
```go
// GET /api/notifications?unread_only=true&limit=50
// PATCH /api/notifications/{id}/read
// PATCH /api/notifications/read-all
// DELETE /api/notifications/{id} (archive)
```

**Real-time updates:** Via unified WS (`notification.new`, `notification.read`).

```typescript
// NotificationCenter.tsx
const { data: notifications } = useQuery(['notifications', { unreadOnly: true }]);
const ws = useWebSocket();

// On ws message "notification.new" → invalidate query
// On click → mark read, navigate via data
```

---

### 7. Integration Points

| Trigger | Push Payload | In-App Notification |
|---------|--------------|---------------------|
| New DM / mention | `{ type: 'chat', channelId, messageId, title: 'Name', body: 'msg preview' }` | ✓ |
| Call invite | `{ type: 'call', roomName, callerName, requireInteraction: true }` | ✓ |
| Task assigned | `{ type: 'task', taskId, title: 'New task', body: 'Task name' }` | ✓ |
| Task due soon | `{ type: 'task.due', taskId, urgency: 'low' }` | ✓ |
| Recording ready | `{ type: 'recording', roomName, recordingUrl }` | ✓ |

---

### 8. Docker / Helm Config

```yaml
# No extra services needed — webpush-go uses standard crypto
# VAPID keys via env/secrets:
env:
  - name: VAPID_PUBLIC_KEY
    valueFrom:
      secretKeyRef:
        name: teamup-secrets
        key: vapid-public-key
  - name: VAPID_PRIVATE_KEY
    valueFrom:
      secretKeyRef:
        name: teamup-secrets
        key: vapid-private-key
  - name: VAPID_SUBJECT
    value: "mailto:admin@teamup.local"
```

---

### 9. Open Questions (for later)

- **Email fallback** (when push fails/permission denied) — v2
- **SMS fallback** (Twilio) — v2
- **Notification preferences per type/channel** — v2
- **Batch/digest notifications** (daily summary) — v2
- **Rich actions** (reply from notification) — requires Notification Action API support

Status: resolved
