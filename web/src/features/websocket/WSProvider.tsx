import { createContext, useContext, useEffect, useRef, useState, useCallback, ReactNode } from 'react';
import { useAuthStore } from '@/shared/store/authStore';
import type { Message, WSMessageType } from '@/shared/types/protocol';

// Module scope so the reconnect backoff is a stable reference across renders.
const CONNECT_DELAYS = [1000, 2000, 5000, 10000, 30000];

interface WSContextType {
  state: 'connecting' | 'ready' | 'closed';
  send: (msg: Omit<Message, 'id' | 'timestamp' | 'version'>) => void;
  on: (type: WSMessageType, handler: (msg: Message) => void) => () => void;
}

const WSContext = createContext<WSContextType | undefined>(undefined);

/** Same host as the page unless told otherwise, so this works behind a proxy. */
function wsOrigin() {
  if (import.meta.env.VITE_WS_URL) return import.meta.env.VITE_WS_URL;
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${window.location.host}`;
}

export function WSProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuthStore();
  const wsRef = useRef<WebSocket | null>(null);
  const [state, setState] = useState<'connecting' | 'ready' | 'closed'>('connecting');
  const handlersRef = useRef<Map<string, Set<(msg: Message) => void>>>(new Map());
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout>>();
  const attemptRef = useRef(0);
  const queueRef = useRef<Array<Omit<Message, 'id' | 'timestamp' | 'version'>>>([]);

  const MAX_ATTEMPTS = 5;

  const connect = useCallback(() => {
    if (!isAuthenticated) return;

    // The access token is in an HttpOnly cookie, so document.cookie cannot see
    // it — reading it here always came back undefined and the socket was never
    // opened. The browser sends the cookie with the handshake by itself, and
    // the server now reads it from there, so no token goes in the URL.
    const wsUrl = `${wsOrigin()}/ws`;
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      setState('connecting');
      ws.send(JSON.stringify({
        id: crypto.randomUUID(),
        type: 'conn.init',
        payload: { clientInfo: { platform: navigator.platform, version: import.meta.env.VITE_APP_VERSION } },
      }));
    };

    ws.onmessage = (event) => {
      try {
        const msg: Message = JSON.parse(event.data);

        if (msg.type === 'conn.ack') {
          setState('ready');
          attemptRef.current = 0;
          queueRef.current.forEach(m => ws.send(JSON.stringify(m)));
          queueRef.current = [];
          return;
        }

        // The server pings every thirty seconds and reaps connections that
        // stop answering, because a client that disappears without closing the
        // socket — a suspended laptop, an expiring NAT entry, a phone changing
        // network — is otherwise indistinguishable from an idle one. Staying
        // silent here would have the server hang up on a perfectly live tab.
        if (msg.type === 'conn.ping') {
          ws.send(JSON.stringify({
            id: crypto.randomUUID(),
            type: 'conn.pong',
            payload: {},
          }));
          return;
        }

        const handlers = handlersRef.current.get(msg.type) || new Set();
        handlers.forEach(h => h(msg));
      } catch (err) {
        console.error('WS message parse error:', err);
      }
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
  }, [isAuthenticated]);

  const send = useCallback((msg: Omit<Message, 'id' | 'timestamp' | 'version'>) => {
    const fullMsg: Message = {
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

  const on = useCallback((type: WSMessageType, handler: (msg: Message) => void) => {
    if (!handlersRef.current.has(type)) {
      handlersRef.current.set(type, new Set());
    }
    handlersRef.current.get(type)!.add(handler);

    return () => {
      handlersRef.current.get(type)?.delete(handler);
    };
  }, []);

  useEffect(() => {
    if (isAuthenticated) {
      connect();
    } else {
      wsRef.current?.close(1000, 'auth lost');
      setState('closed');
    }

    return () => {
      clearTimeout(reconnectTimeoutRef.current);
      wsRef.current?.close(1000, 'unmount');
    };
  }, [connect, isAuthenticated]);

  return (
    <WSContext.Provider value={{ state, send, on }}>
      {children}
    </WSContext.Provider>
  );
}

export function useWebSocket() {
  const context = useContext(WSContext);
  if (!context) {
    throw new Error('useWebSocket must be used within a WSProvider');
  }
  return context;
}
