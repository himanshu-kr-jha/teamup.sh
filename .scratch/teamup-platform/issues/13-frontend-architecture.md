## Question

Define React + PWA frontend architecture: Vite + TypeScript, state management (TanStack Query + Zustand/Redux Toolkit), component library (Radix UI + Tailwind / shadcn-ui), routing (React Router v6), LiveKit React SDK integration, WebSocket client (auto-reconnect, typed messages), Service Worker (Workbox / Vite PWA plugin), IndexedDB wrapper (idb), and build output for Go embed.

Status: claimed

## Answer

**Decision: Frontend architecture defined below.**

### 1. Tech Stack & Project Structure

```
web/
├── public/
│   ├── manifest.json          # PWA manifest
│   ├── icons/                 # PWA icons (192, 512, maskable)
│   └── sw.js                  # Service Worker (generated)
├── src/
│   ├── app/                   # App-level setup
│   │   ├── providers.tsx      # QueryClient, Theme, Auth, WS providers
│   │   ├── router.tsx         # React Router v6 config
│   │   └── routes.tsx         # Route definitions
│   ├── features/              # Feature modules (domain-driven)
│   │   ├── auth/
│   │   ├── chat/
│   │   ├── calls/
│   │   ├── tasks/
│   │   ├── notifications/
│   │   └── settings/
│   ├── shared/                # Shared code
│   │   ├── api/               # API client (TanStack Query)
│   │   ├── components/        # Shared UI components
│   │   ├── hooks/             # Shared hooks
│   │   ├── store/             # Zustand stores
│   │   ├── types/             # TypeScript types
│   │   ├── utils/             # Utilities
│   │   └── constants/         # Constants
│   ├── styles/
│   │   ├── globals.css        # Tailwind + CSS variables
│   │   └── theme.css          # Theme tokens
│   ├── sw/                    # Service Worker registration
│   │   ├── register.ts
│   │   └── types.ts
│   ├── main.tsx               # Entry point
│   └── vite-env.d.ts
├── index.html
├── package.json
├── pnpm-lock.yaml
├── tsconfig.json
├── tsconfig.node.json
├── vite.config.ts
├── tailwind.config.ts
├── postcss.config.js
└── .eslintrc.cjs
```

**Package.json (key deps):**
```json
{
  "dependencies": {
    "react": "^18.2.0",
    "react-dom": "^18.2.0",
    "react-router-dom": "^6.22.0",
    "@tanstack/react-query": "^5.17.0",
    "zustand": "^4.5.0",
    "@livekit/components-react": "^2.1.0",
    "livekit-client": "^2.5.0",
    "@radix-ui/react-*": "^1.0.0",
    "tailwind-merge": "^2.2.0",
    "clsx": "^2.1.0",
    "zod": "^3.22.0",
    "react-hook-form": "^7.50.0",
    "@hookform/resolvers": "^3.3.0",
    "date-fns": "^3.3.0",
    "idb": "^8.0.0",
    "axios": "^1.6.0"
  },
  "devDependencies": {
    "vite": "^5.1.0",
    "@vitejs/plugin-react": "^4.2.0",
    "typescript": "^5.3.0",
    "tailwindcss": "^3.4.0",
    "postcss": "^8.4.0",
    "autoprefixer": "^10.4.0",
    "eslint": "^8.56.0",
    "@typescript-eslint/*": "^7.0.0",
    "vite-plugin-pwa": "^0.19.0",
    "workbox-window": "^7.0.0"
  }
}
```

---

### 2. Vite Config (with PWA + Go Embed)

```typescript
// vite.config.ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { VitePWA } from 'vite-plugin-pwa';
import path from 'path';

export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['favicon.ico', 'robots.txt'],
      manifest: {
        name: 'TeamUp',
        short_name: 'TeamUp',
        description: 'Self-hosted collaboration platform',
        theme_color: '#4f46e5',
        background_color: '#ffffff',
        display: 'standalone',
        orientation: 'portrait-primary',
        scope: '/',
        start_url: '/',
        icons: [
          { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
        ],
        shortcuts: [
          { name: 'Messages', url: '/chat', description: 'Open chat' },
          { name: 'Tasks', url: '/tasks', description: 'Open tasks' },
          { name: 'Calls', url: '/calls', description: 'Start a call' },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,ico,png,svg,woff2}'],
        runtimeCaching: [
          {
            urlPattern: /^https:\/\/.*\/api\/(channels|messages|tasks|users|presence)/,
            handler: 'StaleWhileRevalidate',
            options: {
              cacheName: 'api-get',
              expiration: { maxEntries: 200, maxAgeSeconds: 5 * 60 },
            },
          },
          {
            urlPattern: /^https:\/\/.*\/api\/.*/,
            handler: 'NetworkOnly',
            options: { cacheName: 'api-mutations' },
          },
          {
            urlPattern: /^https:\/\/.*\/livekit\/.*/,
            handler: 'NetworkOnly',
          },
        ],
      },
      devOptions: { enabled: true },
    }),
  ],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  build: {
    outDir: '../cmd/teamup/web/dist',  // Embedded by Go
    emptyOutDir: true,
    sourcemap: true,
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom', 'react-router-dom'],
          query: ['@tanstack/react-query'],
          livekit: ['livekit-client', '@livekit/components-react'],
          ui: ['@radix-ui/react-dialog', '@radix-ui/react-dropdown-menu', '@radix-ui/react-tooltip'],
        },
      },
    },
  },
  server: {
    port: 3000,
    proxy: { '/api': { target: 'http://localhost:8080', changeOrigin: true } },
  },
});
```

---

### 3. State Management

**TanStack Query (Server State):**
```typescript
// shared/api/queryClient.ts
import { QueryClient } from '@tanstack/react-query';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60 * 1000,      // 5 min
      gcTime: 10 * 60 * 1000,        // 10 min (cacheTime)
      retry: 1,
      refetchOnWindowFocus: false,
      refetchOnReconnect: true,
    },
  },
});

// shared/api/client.ts
import axios from 'axios';

export const api = axios.create({
  baseURL: '/api',
  withCredentials: true,  // HttpOnly cookies
});

// Request interceptor: add CSRF if needed
api.interceptors.request.use((config) => {
  const csrf = document.querySelector('meta[name="csrf-token"]')?.getAttribute('content');
  if (csrf) config.headers['X-CSRF-Token'] = csrf;
  return config;
});

// Response interceptor: handle 401 → try refresh
api.interceptors.response.use(
  (r) => r,
  async (error) => {
    if (error.response?.status === 401 && !error.config.url?.includes('/auth/refresh')) {
      try {
        await api.post('/auth/refresh');
        return api.request(error.config);  // Retry
      } catch { /* redirect to login */ }
    }
    return Promise.reject(error);
  }
);
```

**Zustand (Client State):**
```typescript
// shared/store/authStore.ts
import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface User {
  id: string;
  name: string;
  email: string;
  avatarUrl?: string;
  roles: string[];
  orgId: string;
}

interface AuthState {
  user: User | null;
  isAuthenticated: boolean;
  setUser: (user: User | null) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      isAuthenticated: false,
      setUser: (user) => set({ user, isAuthenticated: !!user }),
      logout: () => set({ user: null, isAuthenticated: false }),
    }),
    { name: 'auth-storage', partialize: (s) => ({ user: s.user, isAuthenticated: s.isAuthenticated }) }
  )
);

// shared/store/uiStore.ts
interface UIState {
  sidebarOpen: boolean;
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  activeChannelId: string | null;
  setActiveChannelId: (id: string | null) => void;
  notifications: Notification[];
  addNotification: (n: Notification) => void;
}
```

---

### 4. Component Library (shadcn/ui + Tailwind)

```typescript
// shared/components/ui/button.tsx
import * as React from 'react';
import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from '@/shared/utils/cn';

const buttonVariants = cva(
  'inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50',
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground hover:bg-primary/90',
        destructive: 'bg-destructive text-destructive-foreground hover:bg-destructive/90',
        outline: 'border border-input bg-background hover:bg-accent hover:text-accent-foreground',
        secondary: 'bg-secondary text-secondary-foreground hover:bg-secondary/80',
        ghost: 'hover:bg-accent hover:text-accent-foreground',
        link: 'text-primary underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-10 px-4 py-2',
        sm: 'h-9 rounded-md px-3',
        lg: 'h-11 rounded-md px-8',
        icon: 'h-10 w-10',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  }
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button';
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    );
  }
);
Button.displayName = 'Button';
```

**Tailwind Config:**
```typescript
// tailwind.config.ts
import type { Config } from 'tailwindcss';

export default {
  darkMode: 'class',
  content: ['./src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        border: 'hsl(var(--border))',
        input: 'hsl(var(--input))',
        ring: 'hsl(var(--ring))',
        background: 'hsl(var(--background))',
        foreground: 'hsl(var(--foreground))',
        primary: {
          DEFAULT: 'hsl(var(--primary))',
          foreground: 'hsl(var(--primary-foreground))',
        },
        secondary: { /* ... */ },
        destructive: { /* ... */ },
        muted: { /* ... */ },
        accent: { /* ... */ },
      },
      borderRadius: {
        lg: 'var(--radius)',
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)',
      },
      keyframes: {
        'accordion-down': { from: { height: '0' }, to: { height: 'var(--radix-accordion-content-height)' } },
        'accordion-up': { from: { height: 'var(--radix-accordion-content-height)' }, to: { height: '0' } },
      },
      animation: {
        'accordion-down': 'accordion-down 0.2s ease-out',
        'accordion-up': 'accordion-up 0.2s ease-out',
      },
    },
  },
  plugins: [require('tailwindcss-animate')],
} satisfies Config;
```

---

### 5. Routing (React Router v6)

```typescript
// app/routes.tsx
import { createBrowserRouter, redirect } from 'react-router-dom';
import { Layout } from '@/features/layout/Layout';
import { LoginPage } from '@/features/auth/LoginPage';
import { WizardPage } from '@/features/wizard/WizardPage';
import { ChatPage } from '@/features/chat/ChatPage';
import { CallsPage } from '@/features/calls/CallsPage';
import { TasksPage } from '@/features/tasks/TasksPage';
import { SettingsPage } from '@/features/settings/SettingsPage';
import { useAuthStore } from '@/shared/store/authStore';

const requireAuth = async () => {
  const { isAuthenticated } = useAuthStore.getState();
  if (!isAuthenticated) return redirect('/login');
};

const requireSetup = async () => {
  const res = await fetch('/api/wizard/status');
  if (res.ok) {
    const data = await res.json();
    if (!data.complete) return redirect('/wizard');
  }
};

export const router = createBrowserRouter([
  {
    path: '/',
    element: <Layout />,
    errorElement: <ErrorPage />,
    children: [
      { path: 'login', element: <LoginPage /> },
      { path: 'wizard/*', element: <WizardPage /> },
      {
        path: '/',
        loader: () => Promise.all([requireAuth(), requireSetup()]),
        children: [
          { index: true, element: <Navigate to="/chat" replace /> },
          { path: 'chat', element: <ChatPage /> },
          { path: 'chat/:channelId', element: <ChatPage /> },
          { path: 'calls', element: <CallsPage /> },
          { path: 'calls/:roomName', element: <CallViewPage /> },
          { path: 'tasks', element: <TasksPage /> },
          { path: 'tasks/:taskId', element: <TaskDetailPage /> },
          { path: 'settings', element: <SettingsPage /> },
        ],
      },
    ],
  },
]);
```

---

### 6. WebSocket Client (Auto-reconnect, Typed)

```typescript
// shared/hooks/useWebSocket.ts
import { useEffect, useRef, useCallback, useState } from 'react';
import { useAuthStore } from '@/shared/store/authStore';

type WSMessageType =
  | 'conn.ack' | 'conn.ping' | 'conn.pong' | 'conn.close'
  | 'chat.send' | 'chat.sent' | 'chat.message' | 'chat.edit' | 'chat.delete' | 'chat.typing' | 'chat.read'
  | 'presence.update' | 'presence.changed' | 'presence.sync'
  | 'call.invite' | 'call.invited' | 'call.accept' | 'call.decline' | 'call.ended' | 'call.signaling' | 'call.signaling.relay'
  | 'notification.new' | 'notification.read';

interface WSMessage<T = any> {
  id: string;
  type: WSMessageType;
  payload: T;
  timestamp: string;
  version: number;
}

type MessageHandler = (msg: WSMessage) => void;

export function useWebSocket() {
  const wsRef = useRef<WebSocket | null>(null);
  const [state, setState] = useState<'connecting' | 'ready' | 'closed'>('connecting');
  const handlersRef = useRef<Map<WSMessageType, MessageHandler[]>>(new Map());
  const reconnectTimeoutRef = useRef<NodeJS.Timeout>();
  const attemptRef = useRef(0);
  const queueRef = useRef<WSMessage[]>([]);

  const CONNECT_DELAYS = [1000, 2000, 5000, 10000, 30000];
  const MAX_ATTEMPTS = 5;

  const connect = useCallback(() => {
    const token = document.cookie.split('; ').find(c => c.startsWith('at='))?.split('=')[1];
    if (!token) return;

    const ws = new WebSocket(`${import.meta.env.VITE_WS_URL}?token=${token}`);
    wsRef.current = ws;

    ws.onopen = () => {
      setState('connecting');
      ws.send(JSON.stringify({
        id: crypto.randomUUID(),
        type: 'conn.init',
        payload: { token, clientInfo: { platform: navigator.platform, version: import.meta.env.VITE_APP_VERSION } },
      }));
    };

    ws.onmessage = (event) => {
      const msg: WSMessage = JSON.parse(event.data);

      if (msg.type === 'conn.ack') {
        setState('ready');
        attemptRef.current = 0;
        // Flush queued messages
        queueRef.current.forEach(m => ws.send(JSON.stringify(m)));
        queueRef.current = [];
        return;
      }

      // Route to handlers
      const handlers = handlersRef.current.get(msg.type) || [];
      handlers.forEach(h => h(msg));
    };

    ws.onclose = (e) => {
      setState('closed');
      if (!e.wasClean && attemptRef.current < MAX_ATTEMPTS) {
        const delay = CONNECT_DELAYS[Math.min(attemptRef.current, CONNECT_DELAYS.length - 1)];
        attemptRef.current++;
        reconnectTimeoutRef.current = setTimeout(connect, delay);
      }
    };

    ws.onerror = () => { /* handled by onclose */ };
  }, []);

  const send = useCallback((msg: Omit<WSMessage, 'id' | 'timestamp' | 'version'>) => {
    const fullMsg: WSMessage = {
      ...msg,
      id: crypto.randomUUID(),
      timestamp: new Date().toISOString(),
      version: 1,
    };

    if (wsRef.current?.readyState === WebSocket.OPEN && state === 'ready') {
      wsRef.current.send(JSON.stringify(fullMsg));
    } else {
      queueRef.current.push(fullMsg);
    }
  }, [state]);

  const on = useCallback((type: WSMessageType, handler: MessageHandler) => {
    const handlers = handlersRef.current.get(type) || [];
    handlers.push(handler);
    handlersRef.current.set(type, handlers);
    return () => {
      const h = handlersRef.current.get(type) || [];
      handlersRef.current.set(type, h.filter(fn => fn !== handler));
    };
  }, []);

  const off = useCallback((type: WSMessageType, handler: MessageHandler) => {
    const h = handlersRef.current.get(type) || [];
    handlersRef.current.set(type, h.filter(fn => fn !== handler));
  }, []);

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimeoutRef.current);
      wsRef.current?.close(1000, 'unmount');
    };
  }, [connect]);

  return { state, send, on, off };
}
```

**Usage in Features:**
```typescript
// features/chat/hooks/useChat.ts
export function useChat(channelId: string) {
  const { send, on } = useWebSocket();
  const queryClient = useQueryClient();

  // Listen for new messages
  useEffect(() => {
    const unsubscribe = on('chat.message', (msg) => {
      if (msg.payload.channelId === channelId) {
        queryClient.setQueryData(['messages', channelId], (old: Message[]) => [msg.payload, ...(old || [])]);
      }
    });
    return unsubscribe;
  }, [channelId, on, queryClient]);

  const sendMessage = useCallback((content: string) => {
    const tempId = crypto.randomUUID();
    send({ type: 'chat.send', payload: { channelId, content, tempId } });

    // Optimistic update
    queryClient.setQueryData(['messages', channelId], (old: Message[]) => [
      { id: tempId, channelId, content, userId: currentUser.id, createdAt: Date.now(), pending: true },
      ...(old || []),
    ]);
  }, [channelId, send]);

  return { sendMessage };
}
```

---

### 7. LiveKit React SDK Integration

```typescript
// features/calls/hooks/useLiveKit.ts
import { useRoom, RoomEvent, ConnectionState } from '@livekit/components-react';
import { useAuthStore } from '@/shared/store/authStore';

export function useLiveKitRoom(roomName: string) {
  const { user } = useAuthStore();

  const { room, join, leave, connectionState, localParticipant, remoteParticipants } = useRoom({
    url: import.meta.env.VITE_LIVEKIT_URL,
    token: async () => {
      const res = await api.post('/api/calls/token', { roomName });
      return res.data.token;
    },
    connectOptions: { autoSubscribe: true },
  });

  const toggleMic = useCallback(() =>
    localParticipant?.setMicrophoneEnabled(!localParticipant?.isMicrophoneEnabled()),
    [localParticipant]
  );

  const toggleCamera = useCallback(() =>
    localParticipant?.setCameraEnabled(!localParticipant?.isCameraEnabled()),
    [localParticipant]
  );

  const shareScreen = useCallback(async () => {
    try {
      await localParticipant?.setScreenShareEnabled(true);
    } catch (e) { /* handle error */ }
  }, [localParticipant]);

  return {
    room,
    join,
    leave,
    connectionState,
    isConnected: connectionState === ConnectionState.Connected,
    localParticipant,
    remoteParticipants,
    toggleMic,
    toggleCamera,
    shareScreen,
    isMuted: !localParticipant?.isMicrophoneEnabled(),
    isCameraOff: !localParticipant?.isCameraEnabled(),
  };
}

// features/calls/components/CallView.tsx
import {
  LiveKitRoom,
  VideoConference,
  ParticipantTile,
  Toolbar,
  useParticipants
} from '@livekit/components-react';

export function CallView({ roomName }: { roomName: string }) {
  const { join, leave, isConnected, remoteParticipants, localParticipant, toggleMic, toggleCamera, shareScreen } = useLiveKitRoom(roomName);

  useEffect(() => { join(); }, [join]);

  return (
    <LiveKitRoom url={import.meta.env.VITE_LIVEKIT_URL} token={token} connect={true}>
      <div className="h-full w-full flex flex-col">
        <VideoConference className="flex-1">
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
          onLeave={leave}
        />
      </div>
    </LiveKitRoom>
  );
}
```

---

### 8. IndexedDB Wrapper (idb)

```typescript
// shared/db/indexedDB.ts
import { openDB, DBSchema, IDBPDatabase } from 'idb';

interface TeamUpDB extends DBSchema {
  channels: { key: string; value: Channel; indexes: { 'by-org': string } };
  messages: { key: string; value: Message; indexes: { 'by-channel': string; 'by-user': string; 'by-time': number } };
  tasks: { key: string; value: Task; indexes: { 'by-org': string; 'by-assignee': string; 'by-status': string } };
  users: { key: string; value: User; indexes: { 'by-org': string } };
  outbox: { key: number; value: OutboxItem; autoIncrement: true; indexes: { 'by-type': string; 'by-time': number } };
  pendingUploads: { key: string; value: PendingUpload; indexes: { 'by-status': string } };
  syncMeta: { key: string; value: SyncMeta };
}

let dbPromise: Promise<IDBPDatabase<TeamUpDB>> | null = null;

export function getDB() {
  if (!dbPromise) {
    dbPromise = openDB<TeamUpDB>('teamup-offline', 1, {
      upgrade(db) {
        const ch = db.createObjectStore('channels', { keyPath: 'id' });
        ch.createIndex('by-org', 'orgId');

        const msg = db.createObjectStore('messages', { keyPath: 'id' });
        msg.createIndex('by-channel', 'channelId');
        msg.createIndex('by-user', 'userId');
        msg.createIndex('by-time', 'createdAt');

        const task = db.createObjectStore('tasks', { keyPath: 'id' });
        task.createIndex('by-org', 'orgId');
        task.createIndex('by-assignee', 'assigneeId');
        task.createIndex('by-status', 'status');

        const user = db.createObjectStore('users', { keyPath: 'id' });
        user.createIndex('by-org', 'orgId');

        const outbox = db.createObjectStore('outbox', { keyPath: 'id', autoIncrement: true });
        outbox.createIndex('by-type', 'type');
        outbox.createIndex('by-time', 'timestamp');

        db.createObjectStore('pendingUploads', { keyPath: 'id' });
        db.createObjectStore('syncMeta', { keyPath: 'key' });
      },
    });
  }
  return dbPromise;
}

// Helper functions
export async function getCachedChannels(orgId: string): Promise<Channel[]> {
  const db = await getDB();
  return db.getAllFromIndex('channels', 'by-org', orgId);
}

export async function cacheMessages(channelId: string, messages: Message[]) {
  const db = await getDB();
  const tx = db.transaction('messages', 'readwrite');
  await Promise.all(messages.map(m => tx.store.put(m)));
  await tx.done;
}

export async function addToOutbox(item: Omit<OutboxItem, 'id'>) {
  const db = await getDB();
  return db.add('outbox', { ...item, timestamp: Date.now() });
}
```

---

### 9. Service Worker Registration

```typescript
// sw/register.ts
import { registerSW } from 'virtual:pwa-register';

export function registerServiceWorker() {
  if ('serviceWorker' in navigator) {
    const updateSW = registerSW({
      onNeedRefresh() {
        if (confirm('New version available. Update?')) {
          updateSW(true);
        }
      },
      onOfflineReady() {
        console.log('App ready for offline use');
        showToast('You\'re offline. Changes will sync when reconnected.');
      },
      onRegistered(r) {
        r?.addEventListener('updatefound', () => {
          console.log('SW update found');
        });
      },
      onRegisterError(err) {
        console.error('SW registration failed:', err);
      },
    });
  }
}

// main.tsx
import { registerServiceWorker } from './sw/register';
registerServiceWorker();
```

---

### 10. Offline Banner & Sync Status

```typescript
// shared/components/OfflineBanner.tsx
import { useEffect, useState } from 'react';
import { WifiOff, Loader2 } from 'lucide-react';
import { getDB } from '@/shared/db/indexedDB';

export function OfflineBanner() {
  const [isOnline, setIsOnline] = useState(navigator.onLine);
  const [pendingCount, setPendingCount] = useState(0);

  useEffect(() => {
    const handleOnline = () => setIsOnline(true);
    const handleOffline = () => setIsOnline(false);
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);
    return () => { window.removeEventListener('online', handleOnline); window.removeEventListener('offline', handleOffline); };
  }, []);

  useEffect(() => {
    const interval = setInterval(async () => {
      const db = await getDB();
      const count = await db.count('outbox');
      setPendingCount(count);
    }, 5000);
    return () => clearInterval(interval);
  }, []);

  if (isOnline && pendingCount === 0) return null;

  return (
    <div className={`fixed top-0 left-0 right-0 z-50 px-4 py-2 text-center text-sm font-medium transition-colors ${
      isOnline ? 'bg-yellow-100 text-yellow-800' : 'bg-red-100 text-red-800'
    }`}>
      <div className="flex items-center justify-center gap-2">
        {isOnline ? (
          <>
            <Loader2 className="w-4 h-4 animate-spin" />
            Syncing {pendingCount} pending change{pendingCount !== 1 ? 's' : ''}...
          </>
        ) : (
          <>
            <WifiOff className="w-4 h-4" />
            You're offline. Changes will sync when reconnected.
          </>
        )}
      </div>
    </div>
  );
}
```

---

### 11. Go Embed Integration

```go
// cmd/teamup/main.go
package main

import (
    "embed"
    "io/fs"
    "net/http"
    "github.com/gin-gonic/gin"
)

//go:embed web/dist/*
var webFS embed.FS

func main() {
    r := gin.Default()

    // Serve embedded frontend
    subFS, _ := fs.Sub(webFS, "web/dist")
    r.StaticFS("/static", http.FS(subFS))

    // SPA fallback: serve index.html for non-API routes
    r.NoRoute(func(c *gin.Context) {
        if !strings.HasPrefix(c.Request.URL.Path, "/api/") &&
           !strings.HasPrefix(c.Request.URL.Path, "/ws") &&
           !strings.HasPrefix(c.Request.URL.Path, "/healthz") &&
           !strings.HasPrefix(c.Request.URL.Path, "/metrics") {
            data, _ := webFS.ReadFile("web/dist/index.html")
            c.Data(200, "text/html; charset=utf-8", data)
        }
    })

    // API routes...
    r.Run(":8080")
}
```

**Build Process:**
```bash
# 1. Build frontend
cd web && pnpm run build  # Outputs to ../cmd/teamup/web/dist

# 2. Build Go binary (embeds frontend)
CGO_ENABLED=0 go build -ldflags="-s -w" -o teamup ./cmd/teamup

# Result: Single binary with embedded React app
```

---

### 12. TypeScript Configuration

```json
// tsconfig.json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] },
    "types": ["vite/client", "vite-plugin-pwa/client"]
  },
  "include": ["src"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```

---

### 13. Open Questions (for later)

- **React Native / Capacitor** for native mobile — v2
- **Storybook** for component documentation — v2
- **Visual regression testing** (Chromatic) — v2
- **Micro-frontends** (Module Federation) — v3
- **Server Components** (RSC) — evaluate after React 19

Status: resolved
