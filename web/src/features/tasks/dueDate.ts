/** The destructive token is too dark to read as text on the dark theme's background. */
export const DANGER_TEXT = 'text-red-600 dark:text-red-400';

interface DueFacts {
  status: string;
  dueDate?: string | null;
  /** Set by the server on dashboard and scrum tasks. Wins over the clock when present. */
  overdue?: boolean;
}

export interface DateLabel {
  text: string;
  overdue: boolean;
}

/** "Nov 13", with the year only when it is not this one. */
function shortDate(iso: string, now: Date): string {
  const date = new Date(iso);
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: date.getFullYear() === now.getFullYear() ? undefined : 'numeric',
  });
}

/**
 * The instant to store for a day picked in a date input ("2026-11-06"): the last
 * millisecond of that day here. The server calls a task overdue once its due date
 * has passed, and nothing is late until the day it was due is over.
 */
export function endOfDueDay(day: string): string {
  return new Date(`${day}T23:59:59.999`).toISOString();
}

/** How to word a task's due date, and whether that wording is a warning. */
export function dueLabel(task: DueFacts, now: Date = new Date()): DateLabel | null {
  if (!task.dueDate) return null;
  const overdue = task.status !== 'done' && (task.overdue ?? new Date(task.dueDate) < now);
  return { text: `${overdue ? 'Overdue' : 'Due'} ${shortDate(task.dueDate, now)}`, overdue };
}

/** The one date worth showing on a compact row: when it finished, or else when it is due. */
export function taskDateLabel(
  task: DueFacts & { completedAt?: string | null },
  now: Date = new Date()
): DateLabel | null {
  if (task.status === 'done' && task.completedAt) {
    return { text: `Done ${shortDate(task.completedAt, now)}`, overdue: false };
  }
  return dueLabel(task, now);
}
