import type { QueryClient } from '@tanstack/react-query';

/**
 * For queries about tasks, which other people change underneath you. The app-wide
 * five minute staleTime would make refetchOnWindowFocus a no-op on its own, so
 * both are needed for coming back to the page to show the current state.
 */
export const liveTaskQuery = { staleTime: 0, refetchOnWindowFocus: true } as const;

/** A notification about a task means somebody just changed it. */
export function refreshTasksFor(client: QueryClient, notification: { data?: { taskId?: string } } | undefined) {
  const taskId = notification?.data?.taskId;
  if (!taskId) return;
  client.invalidateQueries({ queryKey: ['tasks'] });
  client.invalidateQueries({ queryKey: ['task', taskId] });
  client.invalidateQueries({ queryKey: ['task-code', taskId] });
  client.invalidateQueries({ queryKey: ['task-activities', taskId] });
  client.invalidateQueries({ queryKey: ['dashboard'] });
}
