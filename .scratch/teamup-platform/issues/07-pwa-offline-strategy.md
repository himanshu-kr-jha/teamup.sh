## Question

Define PWA offline strategy: Service Worker caching strategy (Cache-First for static assets, Stale-While-Revalidate for API GET, Network-Only for mutations), IndexedDB schema for offline read (chats, tasks, user list), optimistic write queue (outbox pattern with background sync), conflict resolution on reconnect, and "offline banner" UX.

Status: claimed

## Answer

**Decision: PWA offline strategy defined below.**

### 1. Service Worker Caching Strategy (Workbox)

```javascript
// sw.js (generated via vite-plugin-pwa + custom)
import { precacheAndRoute, cleanupOutdatedCaches } from 'workbox-precaching';
import { registerRoute, setDefaultHandler } from 'workbox-routing';
import { CacheFirst, StaleWhileRevalidate, NetworkOnly, NetworkFirst } from 'workbox-strategies';
import { ExpirationPlugin } from 'workbox-expiration';
import { BackgroundSyncPlugin } from 'workbox-background-sync';

precacheAndRoute(self.__WB_MANIFEST);
cleanupOutdatedCaches();

// 1. Static assets (JS, CSS, fonts, icons) — CacheFirst, 1 year
registerRoute(
    ({ request }) => request.destination === 'script' ||
                     request.destination === 'style' ||
                     request.destination === 'font',
    new CacheFirst({
        cacheName: 'static-assets',
        plugins: [new ExpirationPlugin({ maxAgeSeconds: 365 * 24 * 60 * 60 })],
    })
);

// 2. Images — CacheFirst, 30 days
registerRoute(
    ({ request }) => request.destination === 'image',
    new CacheFirst({
        cacheName: 'images',
        plugins: [new ExpirationPlugin({ maxEntries: 500, maxAgeSeconds: 30 * 24 * 60 * 60 })],
    })
);

// 3. API GET (read) — StaleWhileRevalidate, 5 min max age
registerRoute(
    ({ url }) => url.pathname.startsWith('/api/') &&
                 ['channels', 'messages', 'tasks', 'users', 'presence'].some(p => url.pathname.includes(p)),
    new StaleWhileRevalidate({
        cacheName: 'api-get',
        plugins: [new ExpirationPlugin({ maxAgeSeconds: 5 * 60 })],
    })
);

// 4. API mutations (POST/PATCH/DELETE) — NetworkOnly + BackgroundSync (outbox)
const bgSyncPlugin = new BackgroundSyncPlugin('outbox-queue', {
    maxRetentionTime: 24 * 60, // 24 hours
    onSync: async ({ queue }) => {
        let entry;
        while ((entry = await queue.shiftRequest())) {
            try {
                await fetch(entry.request);
            } catch (err) {
                await queue.unshiftRequest(entry); // Re-queue on failure
                throw err;
            }
        }
    },
});

registerRoute(
    ({ request }) => request.method !== 'GET' && request.url.includes('/api/'),
    new NetworkOnly({
        plugins: [bgSyncPlugin],
    })
);

// 5. WebSocket / SSE / LiveKit — NetworkOnly (never cache)
registerRoute(
    ({ url }) => url.pathname.startsWith('/ws') || url.pathname.startsWith('/livekit'),
    new NetworkOnly()
);

// Default: NetworkFirst (fallback to cache if offline)
setDefaultHandler(new NetworkFirst({
    cacheName: 'default',
    networkTimeoutSeconds: 5,
    plugins: [new ExpirationPlugin({ maxEntries: 100, maxAgeSeconds: 24 * 60 * 60 })],
}));
```

---

### 2. IndexedDB Schema (idb wrapper)

```typescript
// lib/idb/schema.ts
import { openDB, DBSchema, IDBPDatabase } from 'idb';

interface TeamUpDB extends DBSchema {
    // Cached channels (for sidebar)
    channels: {
        key: string; // channelId
        value: {
            id: string;
            orgId: string;
            name: string;
            type: 'dm' | 'group' | 'public';
            memberIds: string[];
            lastMessageId?: string;
            unreadCount: number;
            updatedAt: number;
        };
        indexes: { 'by-org': string };
    };

    // Cached messages (recent per channel)
    messages: {
        key: string; // messageId
        value: {
            id: string;
            channelId: string;
            userId: string;
            content: string;
            attachments: Attachment[];
            replyTo?: string;
            reactions: Record<string, string[]>;
            createdAt: number;
            editedAt?: number;
            deletedAt?: number;
        };
        indexes: { 'by-channel': string; 'by-user': string; 'by-time': number };
    };

    // Cached tasks
    tasks: {
        key: string; // taskId
        value: {
            id: string;
            orgId: string;
            projectId?: string;
            title: string;
            description: string;
            status: 'todo' | 'in_progress' | 'review' | 'done';
            priority: 'low' | 'medium' | 'high' | 'urgent';
            assigneeId?: string;
            reporterId: string;
            dueDate?: number;
            labels: string[];
            attachments: Attachment[];
            createdAt: number;
            updatedAt: number;
        };
        indexes: { 'by-org': string; 'by-assignee': string; 'by-status': string; 'by-project': string };
    };

    // Cached users (for mentions, presence)
    users: {
        key: string; // userId
        value: {
            id: string;
            orgId: string;
            name: string;
            email: string;
            avatarUrl?: string;
            role: string;
            status: 'online' | 'away' | 'busy' | 'in_call' | 'offline';
            customStatus?: string;
            lastSeen: number;
        };
        indexes: { 'by-org': string };
    };

    // Outbox queue (optimistic writes)
    outbox: {
        key: number; // auto-increment
        value: {
            id: number;
            type: 'chat.send' | 'chat.edit' | 'chat.delete' | 'task.create' | 'task.update' | 'task.delete' | 'presence.update';
            payload: any;
            timestamp: number;
            retryCount: number;
            lastError?: string;
        };
        indexes: { 'by-type': string; 'by-time': number };
    };

    // Pending file uploads (for large files)
    pendingUploads: {
        key: string; // uploadId
        value: {
            id: string;
            file: File; // Stored as Blob
            objectKey: string;
            uploadUrl: string;
            progress: number;
            status: 'pending' | 'uploading' | 'completed' | 'failed';
            createdAt: number;
        };
    };

    // Sync metadata
    syncMeta: {
        key: string;
        value: {
            lastFullSync: number;
            lastChannelsSync: number;
            lastMessagesSync: { [channelId: string]: number };
            lastTasksSync: number;
            lastUsersSync: number;
        };
    };
}

export const dbPromise = openDB<TeamUpDB>('teamup-offline', 1, {
    upgrade(db) {
        // Channels
        const chStore = db.createObjectStore('channels', { keyPath: 'id' });
        chStore.createIndex('by-org', 'orgId');

        // Messages
        const msgStore = db.createObjectStore('messages', { keyPath: 'id' });
        msgStore.createIndex('by-channel', 'channelId');
        msgStore.createIndex('by-user', 'userId');
        msgStore.createIndex('by-time', 'createdAt');

        // Tasks
        const taskStore = db.createObjectStore('tasks', { keyPath: 'id' });
        taskStore.createIndex('by-org', 'orgId');
        taskStore.createIndex('by-assignee', 'assigneeId');
        taskStore.createIndex('by-status', 'status');
        taskStore.createIndex('by-project', 'projectId');

        // Users
        const userStore = db.createObjectStore('users', { keyPath: 'id' });
        userStore.createIndex('by-org', 'orgId');

        // Outbox
        const outboxStore = db.createObjectStore('outbox', { keyPath: 'id', autoIncrement: true });
        outboxStore.createIndex('by-type', 'type');
        outboxStore.createIndex('by-time', 'timestamp');

        // Pending uploads
        db.createObjectStore('pendingUploads', { keyPath: 'id' });

        // Sync meta
        db.createObjectStore('syncMeta', { keyPath: 'key' });
    },
});
```

---

### 3. Offline Read (Cache Population)

```typescript
// hooks/useOfflineData.ts
export function useOfflineChannels(orgId: string) {
    return useQuery({
        queryKey: ['channels', orgId],
        queryFn: async () => {
            const db = await dbPromise;
            return db.getAllFromIndex('channels', 'by-org', orgId);
        },
        staleTime: 5 * 60 * 1000, // 5 min
    });
}

export function useOfflineMessages(channelId: string, limit = 100) {
    return useQuery({
        queryKey: ['messages', channelId],
        queryFn: async () => {
            const db = await dbPromise;
            const all = await db.getAllFromIndex('messages', 'by-channel', channelId);
            return all.sort((a, b) => b.createdAt - a.createdAt).slice(0, limit);
        },
    });
}

export function useOfflineTasks(filters?: { assigneeId?: string; status?: string }) {
    return useQuery({
        queryKey: ['tasks', filters],
        queryFn: async () => {
            const db = await dbPromise;
            let tasks = await db.getAll('tasks');
            if (filters?.assigneeId) tasks = tasks.filter(t => t.assigneeId === filters.assigneeId);
            if (filters?.status) tasks = tasks.filter(t => t.status === filters.status);
            return tasks;
        },
    });
}
```

**Sync on reconnect:**
```typescript
// lib/sync.ts
export async function performFullSync(orgId: string) {
    const db = await dbPromise;
    const meta = await db.get('syncMeta', 'main') || { lastFullSync: 0 };

    // 1. Channels
    const channels = await api.get(`/api/channels?orgId=${orgId}&since=${meta.lastChannelsSync}`);
    for (const ch of channels) await db.put('channels', ch);
    await db.put('syncMeta', { key: 'main', ...meta, lastChannelsSync: Date.now() });

    // 2. Users
    const users = await api.get(`/api/users?orgId=${orgId}&since=${meta.lastUsersSync}`);
    for (const u of users) await db.put('users', u);
    await db.put('syncMeta', { key: 'main', ...meta, lastUsersSync: Date.now() });

    // 3. Messages (per active channel)
    const activeChannels = await db.getAllFromIndex('channels', 'by-org', orgId);
    for (const ch of activeChannels) {
        const since = meta.lastMessagesSync?.[ch.id] || 0;
        const messages = await api.get(`/api/channels/${ch.id}/messages?since=${since}&limit=500`);
        for (const m of messages) await db.put('messages', m);
    }
    // Update sync meta...

    // 4. Tasks
    const tasks = await api.get(`/api/tasks?orgId=${orgId}&since=${meta.lastTasksSync}`);
    for (const t of tasks) await db.put('tasks', t);
    await db.put('syncMeta', { key: 'main', ...meta, lastTasksSync: Date.now(), lastFullSync: Date.now() });
}
```

---

### 4. Optimistic Writes (Outbox Pattern)

```typescript
// hooks/useOptimisticWrite.ts
export function useSendMessage() {
    const ws = useWebSocket();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (msg: { channelId: string; content: string; tempId: string }) => {
            // 1. Optimistic UI update
            const tempMsg = {
                id: msg.tempId,
                channelId: msg.channelId,
                userId: currentUser.id,
                content: msg.content,
                attachments: [],
                reactions: {},
                createdAt: Date.now(),
                pending: true,
            };
            queryClient.setQueryData(['messages', msg.channelId], (old: any[]) => [tempMsg, ...(old || [])]);

            // 2. Queue in outbox (persisted)
            const db = await dbPromise;
            await db.add('outbox', {
                type: 'chat.send',
                payload: { channelId: msg.channelId, content: msg.content, tempId: msg.tempId },
                timestamp: Date.now(),
                retryCount: 0,
            });

            // 3. Try send immediately if online
            if (navigator.onLine && ws.isConnected) {
                ws.send({ type: 'chat.send', payload: { channelId: msg.channelId, content: msg.content, tempId: msg.tempId } });
            }
        },
    });
}

// Background sync processor (runs in SW via workbox-background-sync)
self.addEventListener('sync', (event) => {
    if (event.tag === 'outbox-queue') {
        event.waitUntil(processOutbox());
    }
});

async function processOutbox() {
    const db = await dbPromise;
    const queue = await db.getAllFromIndex('outbox', 'by-time');

    for (const item of queue) {
        try {
            await sendOutboxItem(item);
            await db.delete('outbox', item.id);
        } catch (err) {
            // Increment retry count
            item.retryCount++;
            item.lastError = err.message;
            if (item.retryCount >= 5) {
                // Move to dead letter, notify user
                await db.delete('outbox', item.id);
                await notifyUser(`Failed to send: ${item.payload.content?.slice(0, 50)}`);
            } else {
                await db.put('outbox', item);
            }
        }
    }
}
```

---

### 5. Conflict Resolution on Reconnect

| Operation | Conflict Strategy |
|-----------|-------------------|
| **Chat send** | Server assigns real ID → replace temp ID in cache; if 409 (duplicate), dedupe |
| **Chat edit** | Last-write-wins (server timestamp); show "edited" indicator |
| **Chat delete** | Idempotent — delete if exists |
| **Task update** | Optimistic lock: send `if-match` ETag; on 409, fetch latest, merge, retry |
| **Task status change** | Server authoritative; UI shows server state after sync |
| **Presence** | Always server-wins (ephemeral) |

**Implementation:**
```go
// internal/chat/handler.go (server-side)
func (h *Handler) HandleSendMessage(conn *ws.Connection, msg *IncomingMessage) {
    var payload SendMessagePayload
    json.Unmarshal(msg.Payload, &payload)

    // Create message with server-generated ID
    message := &Message{
        ID:        uuid.New(),
        ChannelID: payload.ChannelID,
        UserID:    conn.userID,
        Content:   payload.Content,
        TempID:    payload.TempID, // For client correlation
    }
    if err := h.chatService.Create(ctx, message); err != nil { ... }

    // Respond with real ID (client replaces temp)
    conn.Enqueue(&OutgoingMessage{
        Type: "chat.sent",
        Payload: map[string]any{
            "tempId": payload.TempID,
            "messageId": message.ID,
            "timestamp": message.CreatedAt,
        },
    })

    // Broadcast to channel
    h.hub.BroadcastToChannel(payload.ChannelID, &OutgoingMessage{
        Type: "chat.message",
        Payload: message,
    }, conn.userID)
}
```

**Client correlation:**
```typescript
// On chat.sent → replace temp message with real one
ws.on('chat.sent', ({ tempId, messageId, timestamp }) => {
    queryClient.setQueryData(['messages', channelId], (old) =>
        old?.map(m => m.id === tempId ? { ...m, id: messageId, createdAt: timestamp, pending: false } : m)
    );
});
```

---

### 6. Offline Banner UX

```tsx
// components/OfflineBanner.tsx
export function OfflineBanner() {
    const [isOnline, setIsOnline] = useState(navigator.onLine);
    const [pendingCount, setPendingCount] = useState(0);

    useEffect(() => {
        const update = () => setIsOnline(navigator.onLine);
        window.addEventListener('online', update);
        window.addEventListener('offline', update);
        return () => { window.removeEventListener('online', update); window.removeEventListener('offline', update); };
    }, []);

    useEffect(() => {
        // Poll outbox count
        const interval = setInterval(async () => {
            const db = await dbPromise;
            const count = await db.count('outbox');
            setPendingCount(count);
        }, 5000);
        return () => clearInterval(interval);
    }, []);

    if (isOnline && pendingCount === 0) return null;

    return (
        <div className={`fixed top-0 left-0 right-0 z-50 px-4 py-2 text-center text-sm ${isOnline ? 'bg-yellow-100' : 'bg-red-100'}`}>
            {isOnline ? (
                <span className="flex items-center justify-center gap-2">
                    <Spinner className="w-4 h-4" />
                    Syncing {pendingCount} pending change{pendingCount !== 1 ? 's' : ''}...
                </span>
            ) : (
                <span className="flex items-center justify-center gap-2">
                    <WifiOff className="w-4 h-4" />
                    You're offline. Changes will sync when reconnected.
                </span>
            )}
        </div>
    );
}
```

---

### 7. Storage Quotas & Cleanup

```typescript
// Estimate storage usage
async function getStorageEstimate() {
    if ('storage' in navigator && 'estimate' in navigator.storage) {
        const { usage, quota } = await navigator.storage.estimate();
        console.log(`Used: ${(usage / 1024 / 1024).toFixed(2)} MB of ${(quota / 1024 / 1024).toFixed(2)} MB`);
    }
}

// Cleanup old messages (keep last 500 per channel)
async function cleanupOldMessages() {
    const db = await dbPromise;
    const channels = await db.getAll('channels');
    for (const ch of channels) {
        const messages = await db.getAllFromIndex('messages', 'by-channel', ch.id);
        if (messages.length > 500) {
            const toDelete = messages.sort((a, b) => a.createdAt - b.createdAt).slice(0, messages.length - 500);
            for (const m of toDelete) await db.delete('messages', m.id);
        }
    }
}
```

---

### 8. Open Questions (for later)

- **Periodic Background Sync** (Periodic Background Sync API) — for proactive sync
- **File upload queue persistence** — large files across sessions
- **Selective sync** (only starred channels/projects) — v2
- **Conflict UI** (show diff for task conflicts) — v2

Status: resolved
