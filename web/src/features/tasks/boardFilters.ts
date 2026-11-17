// What narrows the task board in the browser, beyond what the server is asked
// for: words in the title or key, a priority, and when it is due.

export type MineAs = 'all' | 'assigned' | 'created' | 'added';
export type Mine = 'assigned' | 'created' | 'added';
export type DueFilter = 'any' | 'overdue' | 'week' | 'none';
export type PriorityFilter = 'any' | 'low' | 'medium' | 'high' | 'urgent';

/** What each way a task can be yours is called on its card. */
export const MINE_WORDS: Record<Mine, string> = { assigned: 'Assigned', created: 'Created', added: 'Added' };

interface Filterable {
  key?: string;
  title: string;
  status: string;
  priority: string;
  dueDate?: string | null;
}

/** Whether a task's title or key has every word typed, in any order. */
export function matchesSearch(task: Filterable, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const about = `${task.key ?? ''} ${task.title}`.toLowerCase();
  return words.every((w) => about.includes(w));
}

/** The end of this week: Sunday night, here. */
function endOfWeek(now: Date): Date {
  const end = new Date(now);
  end.setHours(23, 59, 59, 999);
  end.setDate(end.getDate() + ((7 - end.getDay()) % 7));
  return end;
}

export function matchesDue(task: Filterable, due: DueFilter, now = new Date()): boolean {
  if (due === 'any') return true;
  if (due === 'none') return !task.dueDate;
  if (!task.dueDate || task.status === 'done') return false;
  const at = new Date(task.dueDate);
  if (due === 'overdue') return at < now;
  return at >= now && at <= endOfWeek(now);
}

export const matchesPriority = (task: Filterable, priority: PriorityFilter) => priority === 'any' || task.priority === priority;

/** Which of their own tasks somebody last chose to see, kept on this device; all of them at first. */
export function rememberedMineAs(userId: string | undefined): MineAs {
  try {
    const saved = localStorage.getItem(`teamup.tasks.mineAs.${userId ?? ''}`);
    if (saved === 'all' || saved === 'assigned' || saved === 'created' || saved === 'added') return saved;
  } catch {
    // Storage may be off; the default is fine.
  }
  return 'all';
}

export function rememberMineAs(userId: string | undefined, value: MineAs) {
  try {
    localStorage.setItem(`teamup.tasks.mineAs.${userId ?? ''}`, value);
  } catch {
    // Not remembered, then.
  }
}
