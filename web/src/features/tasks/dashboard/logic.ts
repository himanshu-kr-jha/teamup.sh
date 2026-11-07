import type {
  ContributionPerson,
  DashboardPerson,
  DashboardScope,
  Scrum,
  TaskBuckets,
  TaskCounts,
  TaskSummary,
  TeamRef,
} from '@/shared/types/dashboard';
import { formatDate } from '@/shared/utils/cn';

export type TabId = 'overview' | 'person' | 'scrum' | 'history' | 'contribution' | 'board';

export const TAB_LABELS: Record<TabId, string> = {
  overview: 'Overview',
  person: 'By person',
  scrum: 'Scrum',
  history: 'History',
  contribution: 'Contribution',
  board: 'Board',
};

/** Which tabs somebody gets is the server's call, made by the scope it returns. */
export function tabsForScope(scope: DashboardScope): TabId[] {
  return scope === 'self'
    ? ['overview', 'board']
    : ['overview', 'person', 'scrum', 'history', 'contribution', 'board'];
}

/**
 * The tab to show for what the address asks. With the scope still unknown a real
 * tab is honoured, so a reload of a manager's deep link does not bounce to Overview.
 */
export function resolveTab(requested: string | null, scope: DashboardScope | undefined): TabId {
  if (!requested || !(requested in TAB_LABELS)) return 'overview';
  const tab = requested as TabId;
  if (!scope) return tab;
  return tabsForScope(scope).includes(tab) ? tab : 'overview';
}

const PRIORITY_RANK: Record<string, number> = { urgent: 0, high: 1, medium: 2, low: 3 };

type Orderable = Pick<TaskSummary, 'title' | 'priority' | 'overdue' | 'dueDate'>;

/** Most pressing first: overdue, then priority, then nearest due date. */
export function orderTasks<T extends Orderable>(tasks: T[]): T[] {
  const dueAt = (t: T) => (t.dueDate ? new Date(t.dueDate).getTime() : Infinity);
  return [...tasks].sort(
    (a, b) =>
      Number(b.overdue) - Number(a.overdue) ||
      (PRIORITY_RANK[a.priority] ?? 9) - (PRIORITY_RANK[b.priority] ?? 9) ||
      (dueAt(a) === dueAt(b) ? 0 : dueAt(a) - dueAt(b)) ||
      a.title.localeCompare(b.title)
  );
}

function latestFinishedFirst<T extends { completedAt: string | null }>(tasks: T[]): T[] {
  const at = (t: T) => (t.completedAt ? new Date(t.completedAt).getTime() : 0);
  return [...tasks].sort((a, b) => at(b) - at(a));
}

/** Overdue is left out of the sum because those tasks are already counted in their status. */
function hasWork(c: TaskCounts): boolean {
  return c.pending + c.ongoing + c.blocked + c.completed + (c.addedTo ?? 0) > 0;
}

/** Whoever is stuck comes up first and whoever has nothing last, alphabetical otherwise. */
export function orderPeople<T extends { name: string; counts: TaskCounts }>(people: T[]): T[] {
  const rank = (p: T) => (p.counts.blocked > 0 ? 0 : hasWork(p.counts) ? 1 : 2);
  return [...people].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

export function orderContributions(people: ContributionPerson[]): ContributionPerson[] {
  return [...people].sort(
    (a, b) => b.completed - a.completed || b.involved - a.involved || a.name.localeCompare(b.name)
  );
}

export interface BucketSection {
  key: keyof TaskBuckets;
  label: string;
  tasks: TaskSummary[];
}

/** One person's tasks under the spec's headings, in the order the tiles read. */
export function bucketSections(buckets: TaskBuckets): BucketSection[] {
  const all: BucketSection[] = [
    { key: 'pending', label: 'Pending', tasks: orderTasks(buckets.pending) },
    { key: 'ongoing', label: 'Ongoing', tasks: orderTasks(buckets.ongoing) },
    { key: 'blocked', label: 'Blocked', tasks: orderTasks(buckets.blocked) },
    { key: 'overdue', label: 'Overdue', tasks: orderTasks(buckets.overdue) },
    // Somebody else's task they are on, so it stays apart from their own work.
    { key: 'addedTo', label: 'Added to', tasks: orderTasks(buckets.addedTo ?? []) },
    { key: 'completed', label: 'Completed', tasks: latestFinishedFirst(buckets.completed) },
  ];
  return all.filter((section) => section.tasks.length > 0);
}

/** A task as a scrum row shows it, whether it is live or frozen in a saved scrum. */
export interface ScrumTask {
  id: string;
  title: string;
  status: string;
  priority: string;
  overdue: boolean;
  dueDate: string | null;
  completedAt: string | null;
  isNew: boolean;
}

export type ScrumSectionKey = 'blocked' | 'ongoing' | 'pending' | 'addedTo' | 'done';

export interface ScrumSection {
  key: ScrumSectionKey;
  label: string;
  tasks: ScrumTask[];
}

export interface ScrumRow {
  userId: string;
  name: string;
  teams: TeamRef[];
  counts: TaskCounts;
  sections: ScrumSection[];
}

function scrumSections(groups: Record<ScrumSectionKey, ScrumTask[]>): ScrumSection[] {
  const all: ScrumSection[] = [
    { key: 'blocked', label: 'Blocked', tasks: orderTasks(groups.blocked) },
    { key: 'ongoing', label: 'Ongoing', tasks: orderTasks(groups.ongoing) },
    { key: 'pending', label: 'Pending', tasks: orderTasks(groups.pending) },
    { key: 'addedTo', label: 'Added to', tasks: orderTasks(groups.addedTo) },
    { key: 'done', label: 'Done', tasks: latestFinishedFirst(groups.done) },
  ];
  return all.filter((section) => section.tasks.length > 0);
}

/** One person's live work, arranged for a call. Overdue is flagged on the task, not given a heading. */
export function personForScrum(person: DashboardPerson): ScrumRow {
  const isNew = new Set(person.newSinceLastScrum);
  const mark = (tasks: TaskSummary[]): ScrumTask[] => tasks.map((t) => ({ ...t, isNew: isNew.has(t.id) }));
  return {
    userId: person.userId,
    name: person.name,
    teams: person.teams,
    counts: person.counts,
    sections: scrumSections({
      blocked: mark(person.tasks.blocked),
      ongoing: mark(person.tasks.ongoing),
      pending: mark(person.tasks.pending),
      addedTo: mark(person.tasks.addedTo ?? []),
      done: mark(person.tasks.completed),
    }),
  };
}

function sectionForStatus(status: string): ScrumSectionKey {
  if (status === 'blocked') return 'blocked';
  if (status === 'done') return 'done';
  return status === 'todo' ? 'pending' : 'ongoing';
}

/** A saved scrum's flat entries, grouped back into the rows the live view draws. */
export function groupSnapshot(scrum: Pick<Scrum, 'participants' | 'entries'>): ScrumRow[] {
  const rows = scrum.participants.map((participant) => {
    const groups: Record<ScrumSectionKey, ScrumTask[]> = { blocked: [], ongoing: [], pending: [], addedTo: [], done: [] };
    for (const { taskId, userId, addedTo, ...entry } of scrum.entries) {
      if (userId === participant.userId) {
        const section = addedTo ? 'addedTo' : sectionForStatus(entry.status);
        groups[section].push({ ...entry, id: taskId, isNew: false });
      }
    }
    // A scrum saved before added-to existed has nothing to say about it: zero.
    const counts = { ...participant.counts, addedTo: participant.counts.addedTo ?? 0 };
    return { ...participant, counts, teams: [], sections: scrumSections(groups) };
  });
  return orderPeople(rows);
}

export interface DateRange {
  /** Local calendar days, as a date input reads them: YYYY-MM-DD. */
  from: string;
  to: string;
}

const RANGE_DAYS = 30;

function toDayString(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

export function defaultRange(now: Date = new Date()): DateRange {
  const from = new Date(now);
  from.setDate(from.getDate() - RANGE_DAYS);
  return { from: toDayString(from), to: toDayString(now) };
}

function isDay(value: string | null): value is string {
  if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  return toDayString(new Date(`${value}T00:00:00`)) === value;
}

/** The range in the address, with the default standing in for anything that is not a date. */
export function readRange(from: string | null, to: string | null, now: Date = new Date()): DateRange {
  const fallback = defaultRange(now);
  return { from: isDay(from) ? from : fallback.from, to: isDay(to) ? to : fallback.to };
}

export function rangeProblem(range: DateRange): string | null {
  return range.from > range.to ? 'The start date must be on or before the end date.' : null;
}

/** Whole local days, as the instants the API takes. */
export function rangeToApi(range: DateRange): { from: string; to: string } {
  return {
    from: new Date(`${range.from}T00:00:00.000`).toISOString(),
    to: new Date(`${range.to}T23:59:59.999`).toISOString(),
  };
}

/** Where the next page of a list starts, or undefined if that page was the last. */
export function nextBefore(page: { createdAt: string }[], limit: number): string | undefined {
  return page.length === limit ? page[page.length - 1].createdAt : undefined;
}

export const NOTES_MAX_BYTES = 5000;

/** The server limits notes in bytes, and a non-Latin script uses several per letter. */
export function notesByteLength(notes: string): number {
  return new TextEncoder().encode(notes).length;
}

export function scrumLabel(scrum: { teamName: string | null; scope: 'team' | 'org' }): string {
  if (scrum.teamName) return scrum.teamName;
  return scrum.scope === 'org' ? 'Whole organization' : 'All their teams';
}

/** Completed means something different per scope, so the tile has to say which. */
export function completedCaption(scope: DashboardScope, lastScrumAt: string | null): string {
  if (scope === 'self') return 'Completed counts the last 7 days.';
  if (!lastScrumAt) return 'Completed counts the last 7 days, as no scrum has been saved yet.';
  return `Completed counts work since the last scrum on ${formatDate(lastScrumAt)}.`;
}

/**
 * Which ends of a sideways-scrolling strip have something out of sight. A pixel of
 * slack, because a zoomed page reports fractions and the end is never quite reached.
 */
export function hiddenSides(scrollLeft: number, clientWidth: number, scrollWidth: number) {
  return { left: scrollLeft > 1, right: scrollLeft + clientWidth < scrollWidth - 1 };
}
