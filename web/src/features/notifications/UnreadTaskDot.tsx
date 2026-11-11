import { cn } from '@/shared/utils/cn';
import { useUnreadTasks } from './unreadTasks';

/** The mark on a task you have been told something about and not yet opened. */
export function UnreadTaskDot({ taskId, className }: { taskId: string; className?: string }) {
  if (!useUnreadTasks().has(taskId)) return null;
  return (
    // Relative, so the hidden words are placed by the dot: placed by something
    // further up, on a card scrolled out of the board's view they widened the
    // whole page.
    <span className={cn('relative', className)} data-testid="task-unread-dot" title="Updated since you last opened it">
      <span className="block h-2 w-2 rounded-full bg-primary" aria-hidden="true" />
      <span className="sr-only">Updated since you last opened it</span>
    </span>
  );
}
