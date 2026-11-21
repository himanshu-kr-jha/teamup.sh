import type { DashboardPerson, Scrum, ScrumOrder, TaskCounts, TaskSummary, TeamRef } from '@/shared/types/dashboard';
import { orderPeople } from './logic';

/**
 * The scrum as a table, the way a team keeps it in a spreadsheet: one row per
 * person, their lead, and their open work across as Task 1, 2, 3, with what they
 * finished since the last scrum and what is blocked, and on what, at the end.
 *
 * A blocked task leaves Task 1, 2, 3 for the Blocked on column but keeps its
 * place in the viewer's order, so it comes back where it was once unblocked.
 *
 * Whoever looks at it arranges it by hand. Their order wins wherever it says
 * something; anybody or anything it does not mention yet goes after, in the
 * default order, and whatever has gone simply is not drawn.
 */

/** One task in a cell, live or frozen in a saved scrum. */
export interface ScrumCell {
  id: string;
  title: string;
  status: string;
  priority: string;
  overdue: boolean;
  dueDate: string | null;
  completedAt: string | null;
  /** Created since the last scrum. Never set on a saved one. */
  isNew: boolean;
  /** Somebody else's task they were added to, not their own. */
  addedTo: boolean;
  /** What a blocked task is blocked on, in words. Null when not blocked or not recorded. */
  blockedOn: ScrumBlockedOn | null;
}

export interface ScrumBlockedOn {
  /** Empty when no reason was given. */
  note: string;
  who: string | null;
  task: { id: string | null; title: string } | null;
}

export interface ScrumTableRow {
  userId: string;
  name: string;
  teams: TeamRef[];
  /** Their leads' names. Null when a saved scrum did not record them. */
  leads: string[] | null;
  counts: TaskCounts;
  /** Task 1, 2, 3 and on: open work that is not blocked. */
  open: ScrumCell[];
  /** Finished since the last scrum, latest first. */
  done: ScrumCell[];
  /** Blocked work, in the person's order. */
  blocked: ScrumCell[];
  /** Every open task in the person's order, blocked ones included, as the order is kept. */
  order: string[];
}

const PRIORITY_RANK: Record<string, number> = {
  urgent: 0,
  high: 1,
  medium: 2,
  low: 3,
};

/** Put items in the saved order, then the rest in the fallback order. Unknown ids are skipped. */
export function arrange<T>(
  items: T[],
  idOf: (item: T) => string,
  saved: readonly string[] | undefined,
  fallback: (rest: T[]) => T[]
): T[] {
  const byId = new Map(items.map((item) => [idOf(item), item]));
  const placed = new Set<string>();
  const first: T[] = [];
  for (const id of saved ?? []) {
    const item = byId.get(id);
    if (item !== undefined && !placed.has(id)) {
      placed.add(id);
      first.push(item);
    }
  }
  return [...first, ...fallback(items.filter((item) => !placed.has(idOf(item))))];
}

/** Soonest due first and undated last, then the most pressing, then by title. */
function byDue(cells: ScrumCell[]): ScrumCell[] {
  const dueAt = (c: ScrumCell) => (c.dueDate ? new Date(c.dueDate).getTime() : Infinity);
  return [...cells].sort(
    (a, b) =>
      (dueAt(a) === dueAt(b) ? 0 : dueAt(a) - dueAt(b)) ||
      (PRIORITY_RANK[a.priority] ?? 9) - (PRIORITY_RANK[b.priority] ?? 9) ||
      a.title.localeCompare(b.title)
  );
}

/** A row nobody has arranged: their own work by due date, then what they were added to. */
export function defaultTaskOrder(cells: ScrumCell[]): ScrumCell[] {
  return [...byDue(cells.filter((c) => !c.addedTo)), ...byDue(cells.filter((c) => c.addedTo))];
}

function latestFinishedFirst(cells: ScrumCell[]): ScrumCell[] {
  const at = (c: ScrumCell) => (c.completedAt ? new Date(c.completedAt).getTime() : 0);
  return [...cells].sort((a, b) => at(b) - at(a));
}

type CellFacts = Pick<TaskSummary, 'id' | 'title' | 'status' | 'priority' | 'overdue' | 'dueDate' | 'completedAt'>;

function cell(task: CellFacts, extra: Pick<ScrumCell, 'isNew' | 'addedTo' | 'blockedOn'>): ScrumCell {
  return {
    id: task.id,
    title: task.title,
    status: task.status,
    priority: task.priority,
    overdue: task.overdue,
    dueDate: task.dueDate,
    completedAt: task.completedAt,
    ...extra,
  };
}

function liveBlockedOn(t: TaskSummary): ScrumBlockedOn | null {
  if (t.status !== 'blocked') return null;
  const b = t.blockedOn;
  return {
    note: b?.note ?? '',
    who: b?.user?.name ?? null,
    task: b?.task ? { id: b.task.id, title: b.task.title } : null,
  };
}

/** One person's open work, in their order, split into Task 1..N and Blocked on. */
function split(all: ScrumCell[]): Pick<ScrumTableRow, 'open' | 'blocked' | 'order'> {
  return {
    open: all.filter((c) => c.status !== 'blocked'),
    blocked: all.filter((c) => c.status === 'blocked'),
    order: all.map((c) => c.id),
  };
}

/** The live table, in the viewer's own order. */
export function liveScrumRows(people: DashboardPerson[], order: ScrumOrder | undefined): ScrumTableRow[] {
  const rows = people.map((person): ScrumTableRow => {
    const isNew = new Set(person.newSinceLastScrum);
    const mark = (t: TaskSummary, addedTo: boolean) =>
      cell(t, { isNew: isNew.has(t.id), addedTo, blockedOn: liveBlockedOn(t) });
    const own = [...person.tasks.pending, ...person.tasks.ongoing, ...person.tasks.blocked].map((t) => mark(t, false));
    const added = (person.tasks.addedTo ?? []).map((t) => mark(t, true));
    return {
      userId: person.userId,
      name: person.name,
      teams: person.teams,
      leads: (person.leads ?? []).map((l) => l.name),
      counts: { ...person.counts, addedTo: person.counts.addedTo ?? 0 },
      ...split(arrange([...own, ...added], (c) => c.id, order?.tasks[person.userId], defaultTaskOrder)),
      done: latestFinishedFirst(
        person.tasks.completed.map((t) => cell(t, { isNew: false, addedTo: false, blockedOn: null }))
      ),
    };
  });
  return arrange(rows, (r) => r.userId, order?.people, orderPeople);
}

const byPosition = <T extends { position?: number | null }>(items: T[]) =>
  [...items].sort((a, b) => (a.position ?? Infinity) - (b.position ?? Infinity));

/**
 * A saved scrum as the holder had it. One saved before the table kept no order,
 * so it reads in the default order, and no leads.
 */
export function savedScrumRows(scrum: Pick<Scrum, 'participants' | 'entries'>): ScrumTableRow[] {
  const rows = scrum.participants.map((participant): ScrumTableRow & { position?: number | null } => {
    const mine = scrum.entries.filter((e) => e.userId === participant.userId);
    const toCell = (e: (typeof mine)[number]) =>
      cell(
        { ...e, id: e.taskId },
        {
          isNew: false,
          addedTo: !!e.addedTo,
          // Kept as words when it was saved; a scrum from before that has none.
          blockedOn: e.blockedOn
            ? {
                note: e.blockedOn.note,
                who: e.blockedOn.userName,
                task: e.blockedOn.taskTitle ? { id: null, title: e.blockedOn.taskTitle } : null,
              }
            : null,
        }
      );
    const open = mine.filter((e) => e.addedTo || e.status !== 'done');
    const kept = open.some((e) => e.position != null);
    return {
      userId: participant.userId,
      name: participant.name,
      teams: [],
      leads: participant.leads ?? null,
      // A scrum saved before added-to existed has nothing to say about it: zero.
      counts: {
        ...participant.counts,
        addedTo: participant.counts.addedTo ?? 0,
      },
      ...split(kept ? byPosition(open).map(toCell) : defaultTaskOrder(open.map(toCell))),
      done: latestFinishedFirst(mine.filter((e) => !e.addedTo && e.status === 'done').map(toCell)),
      position: participant.position,
    };
  });
  const kept = rows.some((r) => r.position != null);
  return (kept ? byPosition(rows) : orderPeople(rows)).map(({ position: _position, ...row }) => row);
}

/** As many task columns as the busiest person needs, and always at least one. */
export function taskColumnCount(rows: ScrumTableRow[]): number {
  return Math.max(1, ...rows.map((r) => r.open.length));
}

/** The order on screen, as a saved scrum should record it. */
export function orderOf(rows: ScrumTableRow[]): ScrumOrder {
  return {
    people: rows.map((r) => r.userId),
    tasks: Object.fromEntries(rows.map((r) => [r.userId, r.order])),
  };
}

/**
 * A person's whole order after their Task 1..N were rearranged to `shown`: the
 * blocked tasks, which are not among them, stay in the places they had, and the
 * rest fill the other places in their new order.
 */
export function withHiddenKept(order: readonly string[], shown: readonly string[]): string[] {
  const visible = new Set(shown);
  const next = [...shown];
  const out = order.map((id) => (visible.has(id) ? next.shift()! : id));
  // Anything shown that the old order did not have yet goes at the end.
  return [...out, ...next];
}

/** The list with the item at `from` moved to `to`. */
export function moved<T>(items: readonly T[], from: number, to: number): T[] {
  const out = [...items];
  if (from < 0 || from >= out.length || to < 0 || to >= out.length) return out;
  const [item] = out.splice(from, 1);
  out.splice(to, 0, item);
  return out;
}

export type CellStatus = { label: string; danger: boolean };

/**
 * What a cell says about a task's state. Blocked is the danger here; a late task
 * says so on its date, and the cell is marked either way.
 */
export function cellStatus(c: Pick<ScrumCell, 'status'>): CellStatus {
  switch (c.status) {
    case 'blocked':
      return { label: 'Blocked', danger: true };
    case 'in_progress':
      return { label: 'Ongoing', danger: false };
    case 'in_review':
      return { label: 'In review', danger: false };
    case 'done':
      return { label: 'Done', danger: false };
    default:
      return { label: 'Pending', danger: false };
  }
}
