import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { presenceApi } from '@/shared/api/client';
import { useWebSocket } from '@/features/websocket/WSProvider';

export type PresenceStatus = 'online' | 'away' | 'busy' | 'offline';

/**
 * Who is online. The presence service and its endpoints already existed and
 * the socket already carried updates; nothing in the interface used any of it.
 */
export function usePresence() {
  const queryClient = useQueryClient();
  const { on } = useWebSocket();

  const { data } = useQuery({
    queryKey: ['presence'],
    queryFn: () => presenceApi.listOnline(),
    // A fallback in case an update is missed; the socket does the real work.
    refetchInterval: 60_000,
  });

  useEffect(() => {
    return on('presence.update', () => {
      queryClient.invalidateQueries({ queryKey: ['presence'] });
    });
  }, [on, queryClient]);

  const online = new Map<string, PresenceStatus>();
  for (const entry of data?.data ?? []) {
    const e = entry as any;
    online.set(e.userId ?? e.id, (e.status as PresenceStatus) ?? 'online');
  }

  return {
    statusOf: (userId?: string): PresenceStatus =>
      (userId && online.get(userId)) || 'offline',
  };
}
