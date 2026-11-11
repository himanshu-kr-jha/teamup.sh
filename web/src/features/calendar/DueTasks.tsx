import { Link } from 'react-router-dom';
import { isSameDay } from 'date-fns';
import { CheckSquare } from 'lucide-react';
import type { DueTask } from '@/shared/api/client';
import { cn } from '@/shared/utils/cn';

/** The tasks due on a day, by the day they fall due here. */
export function tasksDueOn(tasks: DueTask[], day: Date): DueTask[] {
  return tasks.filter((t) => isSameDay(new Date(t.dueDate), day));
}

/** One task on the day it is due: opens the task. Done is struck through, late is red. */
export function DueChip({ task, now, compact }: { task: DueTask; now: Date; compact?: boolean }) {
  const done = task.status === 'done';
  const late = !done && new Date(task.dueDate) < now;
  return (
    <Link
      to={`/tasks/${task.id}`}
      className={cn(
        'pointer-events-auto flex w-full min-w-0 items-center gap-1 rounded border px-1 py-0.5 text-left text-xs',
        late
          ? 'border-red-500/50 bg-red-50 text-red-700 hover:bg-red-100 dark:bg-red-950/40 dark:text-red-300 dark:hover:bg-red-950/60'
          : 'border-border bg-muted/60 text-foreground hover:bg-accent',
        done && 'text-muted-foreground'
      )}
      data-testid="due-task"
      title={`${task.key ? `${task.key} ` : ''}${task.title}, ${done ? 'done' : late ? 'overdue' : 'due'}`}
    >
      <CheckSquare className="h-3 w-3 shrink-0" aria-hidden="true" />
      {!compact && task.key && <span className="shrink-0 font-mono text-[10px] opacity-70">{task.key}</span>}
      <span className={cn('min-w-0 truncate', done && 'line-through')}>{task.title}</span>
      <span className="sr-only">{done ? ', done' : late ? ', overdue' : ', due'}</span>
    </Link>
  );
}
