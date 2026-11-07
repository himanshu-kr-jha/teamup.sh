import { createContext, useContext, useEffect, useRef, useState, useCallback, ReactNode } from 'react';
import { useAuthStore } from '@/shared/store/authStore';
import type { Message, WSMessageType } from '@/shared/types/protocol';
import { newId } from '@/shared/utils/id';
import { reconnectDelay, shouldReconnect } from './backoff';

/**
 * 'reconnecting' is a socket that was working and is not right now, with
 * another attempt already scheduled. It is separate from 'connecting' because
 * the UI has something to say about it: a channel that has gone quiet because
 * the connection dropped looks exactly like a channel where nobody is talking.
 */
export type WSState = 'connecting' | 'ready' | 'reconnecting' | 'closed';

interface WSContextType {
  state: WSState;
  /** How many attempts have failed in a row. 0 whenever the socket is up. */
  retryCount: number;
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
  const [state, setState] = useState<WSState>('connecting');
  const [retryCount, setRetryCount] = useState(0);
  const handlersRef = useRef<Map<string, Set<(msg: Message) => void>>>(new Map());
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout>>();
  const attemptRef = useRef(0);
  const queueRef = useRef<Array<Omit<Message, 'id' | 'timestamp' | 'version'>>>([]);
  // Set only when this tab closes the socket on purpose — logging out, or
  // going away. Everything else is a drop, and a drop is reconnected.
  const intentionalRef = useRef(false);
  // The retry timer holds this rather than the callback it was scheduled
  // with, so a reconnect scheduled thirty seconds ago still calls the current
  // one. Assigned below, once there is a `connect` to point it at.
  const connectRef = useRef<() => void>(() => {});

  const connect = useCallback(() => {
    if (!isAuthenticated) return;
    // A scheduled retry and a nudge from `online` or a tab becoming visible
    // can arrive together; opening two sockets would have the server reap one
    // and this code treat that as another drop.
    clearTimeout(reconnectTimeoutRef.current);
    const existing = wsRef.current;
    if (existing && (existing.readyState === WebSocket.OPEN || existing.readyState === WebSocket.CONNECTING)) {
      return;
    }
    intentionalRef.current = false;

    // The access token is in an HttpOnly cookie, so document.cookie cannot see
    // it — reading it here always came back undefined and the socket was never
    // opened. The browser sends the cookie with the handshake by itself, and
    // the server now reads it from there, so no token goes in the URL.
    const wsUrl = `${wsOrigin()}/ws`;
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      if (wsRef.current !== ws) return;
      setState(attemptRef.current > 0 ? 'reconnecting' : 'connecting');
      ws.send(JSON.stringify({
        id: newId(),
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
          setRetryCount(0);
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
            id: newId(),
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
      // A socket we already replaced closing late must not schedule anything.
      if (wsRef.current !== ws) return;
      wsRef.current = null;

      if (!shouldReconnect(e, intentionalRef.current)) {
        setState('closed');
        return;
      }

      // Never stops trying. The server drains and closes cleanly on shutdown,
      // so the well-behaved deploy used to be the one that killed every tab:
      // clean close, no retry, silently dead until somebody reloaded.
      const delay = reconnectDelay(attemptRef.current);
      attemptRef.current++;
      setRetryCount(attemptRef.current);
      setState('reconnecting');
      reconnectTimeoutRef.current = setTimeout(() => connectRef.current(), delay);
    };

    ws.onerror = () => { /* handled by onclose */ };
  }, [isAuthenticated]);

  connectRef.current = connect;

  /**
   * Two things say "the network is probably back" sooner than the timer will:
   * the browser regaining connectivity, and somebody returning to the tab.
   * Without these a laptop opened after lunch waits out whatever remains of a
   * thirty-second backoff before it even tries.
   */
  useEffect(() => {
    if (!isAuthenticated) return;
    const nudge = () => {
      if (document.visibilityState !== 'visible' || !navigator.onLine) return;
      if (wsRef.current) return;
      // Straight back to the top of the ladder: this is new information, not
      // another failed attempt.
      attemptRef.current = 0;
      connectRef.current();
    };
    window.addEventListener('online', nudge);
    document.addEventListener('visibilitychange', nudge);
    return () => {
      window.removeEventListener('online', nudge);
      document.removeEventListener('visibilitychange', nudge);
    };
  }, [isAuthenticated]);

  const send = useCallback((msg: Omit<Message, 'id' | 'timestamp' | 'version'>) => {
    const fullMsg: Message = {
      ...msg,
      id: newId(),
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
      // Deliberate: without a session there is nothing to reconnect to, and
      // retrying for ever would be a login page quietly hammering /ws.
      intentionalRef.current = true;
      clearTimeout(reconnectTimeoutRef.current);
      wsRef.current?.close(1000, 'auth lost');
      wsRef.current = null;
      attemptRef.current = 0;
      setRetryCount(0);
      setState('closed');
    }

    return () => {
      intentionalRef.current = true;
      clearTimeout(reconnectTimeoutRef.current);
      wsRef.current?.close(1000, 'unmount');
      wsRef.current = null;
    };
  }, [connect, isAuthenticated]);

  return (
    <WSContext.Provider value={{ state, retryCount, send, on }}>
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
