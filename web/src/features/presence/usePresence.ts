import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { presenceApi } from '@/shared/api/client';
import { useWebSocket } from '@/features/websocket/WSProvider';
import type { WSMessageType } from '@/shared/types/protocol';

/**
 * The frame the server broadcasts when somebody's status changes.
 *
 * Named rather than written inline because the protocol has two presence
 * types that read almost the same and travel in opposite directions:
 * `presence.update` is what a client sends the server, and this is what the
 * server sends back out. Subscribing to the wrong one is silent — the socket
 * accepts any type and simply never fires the handler.
 */
export const SERVER_PRESENCE_EVENT: WSMessageType = 'presence.changed';

/**
 * How long presence frames are gathered up before the roster is refetched.
 *
 * The frame says who changed but not what to show: the roster folds in leave,
 * whether they are in a call, and everybody's chosen note, so the answer has
 * to come from the server. Refetching on each frame would turn one person
 * opening a laptop into one request per open tab, and a standup starting into
 * a burst of them. Two seconds is under what anybody notices next to a status
 * dot and collapses a whole arrival into a single request.
 */
export const PRESENCE_COALESCE_MS = 2_000;

/**
 * What a colleague sees. Deliberately four states: somebody not connected is
 * away, and somebody in a call is busy — the older names are kept only so a
 * cached response still renders.
 */
export type PresenceStatus =
  | 'available'
  | 'away'
  | 'busy'
  | 'on_leave'
  /** @deprecated the server sends busy instead */
  | 'in_call'
  /** @deprecated the server sends away instead */
  | 'offline';

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
    let pending: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = on(SERVER_PRESENCE_EVENT, () => {
      if (pending) return;
      pending = setTimeout(() => {
        pending = undefined;
        queryClient.invalidateQueries({ queryKey: ['availability'] });
      }, PRESENCE_COALESCE_MS);
    });
    return () => {
      if (pending) clearTimeout(pending);
      unsubscribe();
    };
  }, [on, queryClient]);

  const people = new Map<string, Person>();
  for (const entry of (data?.data ?? []) as Person[]) {
    people.set(entry.userId, entry);
  }

  return {
    statusOf: (userId?: string): PresenceStatus =>
      (userId && people.get(userId)?.status) || 'away',
    personOf: (userId?: string): Person | undefined =>
      userId ? people.get(userId) : undefined,
    everybody: [...people.values()],
  };
}
