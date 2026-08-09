import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { presenceApi } from '@/shared/api/client';
import { useWebSocket } from '@/features/websocket/WSProvider';

export type PresenceStatus = 'available' | 'away' | 'busy' | 'in_call' | 'on_leave' | 'offline';

export interface Person {
  userId: string;
  name: string;
  status: PresenceStatus;
  /** What they wrote about it — "back on Monday", and so on. */
  note?: string;
  until?: string;
}

/**
 * Who is around, and what they have said about it.
 *
 * Availability covers everybody in the organization rather than only the
 * people connected right now: somebody on leave has to read as on leave, and
 * that is exactly when they are not here.
 */
export function usePresence() {
  const queryClient = useQueryClient();
  const { on } = useWebSocket();

  const { data } = useQuery({
    queryKey: ['availability'],
    queryFn: () => presenceApi.listAvailability(),
    // A fallback in case an update is missed; the socket does the real work.
    refetchInterval: 60_000,
  });

  useEffect(() => {
    return on('presence.update', () => {
      queryClient.invalidateQueries({ queryKey: ['availability'] });
    });
  }, [on, queryClient]);

  const people = new Map<string, Person>();
  for (const entry of (data?.data ?? []) as Person[]) {
    people.set(entry.userId, entry);
  }

  return {
    statusOf: (userId?: string): PresenceStatus =>
      (userId && people.get(userId)?.status) || 'offline',
    personOf: (userId?: string): Person | undefined =>
      userId ? people.get(userId) : undefined,
    everybody: [...people.values()],
  };
}
