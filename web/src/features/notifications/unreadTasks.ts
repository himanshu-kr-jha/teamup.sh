import { useCallback, useEffect, useRef } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { notificationsApi } from '@/shared/api/client';
import { useWebSocket } from '@/features/websocket/WSProvider';

/** Under ['notifications'], so whatever refreshes the bell refreshes this too. */
export const UNREAD_TASKS_KEY = ['notifications', 'unread-tasks'] as const;

/**
 * The tasks you have been told something about and not yet opened: the number
 * on Tasks, and the dot on each of them wherever they are listed. Tasks, not
 * notifications, because "3" should mean three tasks to look at.
 */
export function useUnreadTasks(): Set<string> {
  const { data } = useQuery({
    queryKey: UNREAD_TASKS_KEY,
    queryFn: () => notificationsApi.unreadTasks(),
    select: toSet,
    staleTime: 30_000,
  });
  return data ?? EMPTY;
}

const EMPTY = new Set<string>();

// Outside the hook, so every card listening shares one Set rather than building its own.
function toSet(res: { data?: { taskIds?: string[] } }): Set<string> {
  return new Set(res.data?.taskIds ?? []);
}

/**
 * Reads a task's notifications while it is open in front of you: on opening it,
 * on coming back to the tab, and when something new about it arrives meanwhile.
 */
export function useReadTaskWhileOpen(taskId: string | undefined) {
  const queryClient = useQueryClient();
  const { on } = useWebSocket();
  const unread = useUnreadTasks();
  const hasUnread = !!taskId && unread.has(taskId);
  const unreadRef = useRef(hasUnread);
  unreadRef.current = hasUnread;

  const read = useCallback(() => {
    if (!taskId || document.visibilityState !== 'visible') return;
    void notificationsApi
      .markTaskRead(taskId)
      .then(() => queryClient.invalidateQueries({ queryKey: ['notifications'] }))
      .catch(() => {
        // The badge stays until the next time; nothing else depends on it.
      });
  }, [taskId, queryClient]);

  useEffect(() => {
    if (hasUnread) read();
  }, [hasUnread, read]);

  useEffect(() => {
    if (!taskId) return;
    const stop = on('notification.new', (msg) => {
      const about = (msg.payload as { data?: { taskId?: string } })?.data?.taskId;
      if (about === taskId) read();
    });
    const back = () => {
      if (unreadRef.current) read();
    };
    document.addEventListener('visibilitychange', back);
    return () => {
      stop();
      document.removeEventListener('visibilitychange', back);
    };
  }, [taskId, on, read]);
}
