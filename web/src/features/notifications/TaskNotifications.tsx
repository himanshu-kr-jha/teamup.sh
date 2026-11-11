import { useEffect, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { useWebSocket } from '@/features/websocket/WSProvider';
import { useChime } from '@/shared/hooks/useChime';
import { useDesktopNotifications } from '@/shared/hooks/useDesktopNotifications';

/**
 * Makes news about a task heard wherever you are in the app, as a message is:
 * the message sound, and a desktop notification. Not while the task itself is
 * open in front of you, which already shows what happened.
 *
 * Also keeps every badge right when a notification is read somewhere else: in
 * another tab, or on another device.
 */
export function TaskNotifications() {
  const { on } = useWebSocket();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const { ping } = useChime();
  const { notify } = useDesktopNotifications();

  const pathnameRef = useRef(location.pathname);
  pathnameRef.current = location.pathname;

  useEffect(() => {
    const stops = [
      on('notification.new', (msg) => {
        const n = msg.payload as { type?: string; title?: string; body?: string; data?: { taskId?: string } };
        const taskId = n?.data?.taskId;
        if (!n?.type?.startsWith('task.') || !taskId) return;
        const lookingAtIt = document.visibilityState === 'visible' && pathnameRef.current === `/tasks/${taskId}`;
        if (lookingAtIt) return;
        ping();
        notify({
          title: n.title || 'A task changed',
          body: n.body ?? '',
          // One per task: a burst of changes replaces itself.
          tag: `task-${taskId}`,
          url: `/tasks/${taskId}`,
          onClick: () => navigate(`/tasks/${taskId}`),
          // Every notification is pushed to the person's devices too.
          alsoPushed: true,
        });
      }),
      on('notification.read', () => {
        void queryClient.invalidateQueries({ queryKey: ['notifications'] });
      }),
    ];
    return () => stops.forEach((stop) => stop());
  }, [on, queryClient, navigate, ping, notify]);

  return null;
}
