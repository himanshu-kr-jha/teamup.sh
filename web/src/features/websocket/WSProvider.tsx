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

    const token = document.cookie.split('; ').find(c => c.startsWith('at='))?.split('=')[1];
    if (!token) return;

    const wsUrl = `${import.meta.env.VITE_WS_URL || 'ws://localhost:8080'}/ws?token=${token}`;
    const ws = new WebSocket(wsUrl);
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
      try {
        const msg: Message = JSON.parse(event.data);

        if (msg.type === 'conn.ack') {
          setState('ready');
          attemptRef.current = 0;
          queueRef.current.forEach(m => ws.send(JSON.stringify(m)));
          queueRef.current = [];
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
