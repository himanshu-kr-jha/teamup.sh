import { describe, expect, it } from 'vitest';
import type { DashboardPerson, Scrum, ScrumEntry, TaskCounts, TaskSummary } from '@/shared/types/dashboard';
import {
  arrange,
  cellStatus,
  defaultTaskOrder,
  liveScrumRows,
  moved,
  orderOf,
  savedScrumRows,
  taskColumnCount,
} from './scrumTable';

const noCounts: TaskCounts = {
  pending: 0,
  ongoing: 0,
  blocked: 0,
  overdue: 0,
  completed: 0,
  addedTo: 0,
};
const noTasks = {
  pending: [],
  ongoing: [],
  blocked: [],
  overdue: [],
  completed: [],
  addedTo: [],
};

function task(over: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: over.id ?? over.title ?? 't',
    title: over.title ?? over.id ?? 'Task',
    status: 'todo',
    priority: 'medium',
    overdue: false,
    dueDate: null,
    completedAt: null,
    createdAt: '2026-11-01T00:00:00Z',
    ...over,
  };
}

function person(over: Partial<DashboardPerson> = {}): DashboardPerson {
  return {
    userId: over.name ?? 'u',
    name: 'Someone',
    teams: [],
    leads: [],
    counts: noCounts,
    tasks: noTasks,
    newSinceLastScrum: [],
    ...over,
  };
}

const ids = (items: { id: string }[]) => items.map((i) => i.id);
const names = (rows: { name: string }[]) => rows.map((r) => r.name);

describe('arrange', () => {
  const items = ['a', 'b', 'c', 'd'].map((id) => ({ id }));
  const alphabetical = (rest: { id: string }[]) => [...rest].sort((x, y) => x.id.localeCompare(y.id));

  it('puts the saved order first and everything it does not mention after, in the fallback order', () => {
    expect(ids(arrange(items, (i) => i.id, ['c', 'a'], alphabetical))).toEqual(['c', 'a', 'b', 'd']);
  });

  it('skips ids that are gone and ids said twice', () => {
    expect(ids(arrange(items, (i) => i.id, ['gone', 'd', 'd', 'b'], alphabetical))).toEqual(['d', 'b', 'a', 'c']);
  });

  it('is the fallback order when nothing is saved', () => {
    expect(ids(arrange([...items].reverse(), (i) => i.id, undefined, alphabetical))).toEqual(['a', 'b', 'c', 'd']);
  });
});

describe('the default order of a row', () => {
  it('is their own work by due date with undated last, then what they were added to', () => {
    const p = person({
      tasks: {
        ...noTasks,
        pending: [task({ id: 'undated' }), task({ id: 'later', dueDate: '2026-11-20T00:00:00Z' })],
        blocked: [
          task({
            id: 'soon',
            status: 'blocked',
            dueDate: '2026-11-10T00:00:00Z',
          }),
        ],
        addedTo: [task({ id: 'theirs', dueDate: '2026-11-01T00:00:00Z' })],
      },
    });
    expect(ids(liveScrumRows([p], undefined)[0].open)).toEqual(['soon', 'later', 'undated', 'theirs']);
  });

  it('breaks a tie of dates by priority, then title', () => {
    const due = '2026-11-10T00:00:00Z';
    const cells = liveScrumRows(
      [
        person({
          tasks: {
            ...noTasks,
            pending: [
              task({ id: 'b', dueDate: due }),
              task({ id: 'a', dueDate: due }),
              task({ id: 'u', dueDate: due, priority: 'urgent' }),
            ],
          },
        }),
      ],
      undefined
    )[0].open;
    expect(ids(defaultTaskOrder(cells))).toEqual(['u', 'a', 'b']);
  });
});

describe('liveScrumRows', () => {
  const alice = person({
    userId: 'alice',
    name: 'Alice',
    leads: [
      { id: 'm1', name: 'Lena' },
      { id: 'm2', name: 'Avi' },
    ],
    counts: { ...noCounts, pending: 2, blocked: 1, completed: 1, addedTo: 1 },
    tasks: {
      ...noTasks,
      pending: [task({ id: 'a1' }), task({ id: 'a2' })],
      blocked: [task({ id: 'a3', status: 'blocked' })],
      completed: [
        task({
          id: 'older',
          status: 'done',
          completedAt: '2026-11-01T00:00:00Z',
        }),
        task({
          id: 'newer',
          status: 'done',
          completedAt: '2026-11-05T00:00:00Z',
        }),
      ],
      addedTo: [task({ id: 'theirs', status: 'in_progress' })],
    },
    newSinceLastScrum: ['a2'],
  });
  const ben = person({
    userId: 'ben',
    name: 'Ben',
    counts: { ...noCounts, blocked: 1 },
    tasks: { ...noTasks, blocked: [task({ id: 'b1', status: 'blocked' })] },
  });
  const cara = person({ userId: 'cara', name: 'Cara' });

  it('spreads open work across the row, keeps done apart, and marks new and added-to work', () => {
    const [row] = liveScrumRows([alice], undefined);
    expect(row.leads).toEqual(['Lena', 'Avi']);
    expect(ids(row.open)).toEqual(['a1', 'a2', 'a3', 'theirs']);
    expect(row.open.find((c) => c.id === 'a2')?.isNew).toBe(true);
    expect(row.open.find((c) => c.id === 'theirs')?.addedTo).toBe(true);
    expect(row.open.find((c) => c.id === 'a1')?.addedTo).toBe(false);
    expect(ids(row.done)).toEqual(['newer', 'older']);
  });

  it('orders people as the dashboard does until the viewer arranges them', () => {
    expect(names(liveScrumRows([cara, alice, ben], undefined))).toEqual(['Alice', 'Ben', 'Cara']);
  });

  it("follows the viewer's order, with anybody new at the end and anybody gone left out", () => {
    const order = {
      people: ['cara', 'gone', 'alice'],
      tasks: { alice: ['theirs', 'a3', 'gone'] },
    };
    const rows = liveScrumRows([alice, ben, cara], order);
    expect(names(rows)).toEqual(['Cara', 'Alice', 'Ben']);
    expect(ids(rows[1].open)).toEqual(['theirs', 'a3', 'a1', 'a2']);
  });

  it('copes with a person from before leads were sent', () => {
    const old = { ...cara, leads: undefined } as unknown as DashboardPerson;
    expect(liveScrumRows([old], undefined)[0].leads).toEqual([]);
  });

  it('records what is on screen for a saved scrum', () => {
    const rows = liveScrumRows([alice, cara], {
      people: ['cara'],
      tasks: { alice: ['a3'] },
    });
    expect(orderOf(rows)).toEqual({
      people: ['cara', 'alice'],
      tasks: { cara: [], alice: ['a3', 'a1', 'a2', 'theirs'] },
    });
  });
});

describe('taskColumnCount', () => {
  it('is as many columns as the busiest person needs, never fewer than one, and never capped', () => {
    const busy = person({
      tasks: {
        ...noTasks,
        pending: Array.from({ length: 9 }, (_, i) => task({ id: `t${i}` })),
      },
    });
    expect(taskColumnCount(liveScrumRows([busy, person({ userId: 'idle' })], undefined))).toBe(9);
    expect(taskColumnCount(liveScrumRows([person()], undefined))).toBe(1);
    expect(taskColumnCount([])).toBe(1);
  });
});

describe('savedScrumRows', () => {
  const entry = (userId: string, taskId: string, over: Partial<ScrumEntry> = {}): ScrumEntry => ({
    userId,
    taskId,
    title: taskId,
    status: 'todo',
    priority: 'medium',
    overdue: false,
    dueDate: null,
    completedAt: null,
    ...over,
  });
  const scrum = (over: Partial<Scrum>): Scrum => ({
    id: 's',
    teamId: null,
    teamName: null,
    scope: 'team',
    heldById: null,
    heldByName: 'Boss',
    notes: '',
    doneSince: '2026-11-01T00:00:00Z',
    createdAt: '2026-11-06T00:00:00Z',
    participantCount: 0,
    entryCount: 0,
    participants: [],
    entries: [],
    ...over,
  });

  it("reads back in the holder's order, with their leads", () => {
    const rows = savedScrumRows(
      scrum({
        participants: [
          {
            userId: 'a',
            name: 'Ann',
            counts: noCounts,
            position: 1,
            leads: ['Lena'],
          },
          {
            userId: 'z',
            name: 'Zed',
            counts: noCounts,
            position: 0,
            leads: [],
          },
        ],
        entries: [
          entry('a', 'a1', { position: 1 }),
          entry('a', 'a2', { position: 0, status: 'blocked' }),
          entry('a', 'a3', {
            position: 2,
            addedTo: true,
            status: 'in_progress',
          }),
          entry('a', 'done', {
            status: 'done',
            position: null,
            completedAt: '2026-11-05T00:00:00Z',
          }),
        ],
      })
    );
    expect(names(rows)).toEqual(['Zed', 'Ann']);
    expect(rows[1].leads).toEqual(['Lena']);
    expect(rows[0].leads).toEqual([]);
    expect(ids(rows[1].open)).toEqual(['a2', 'a1', 'a3']);
    expect(rows[1].open[2].addedTo).toBe(true);
    expect(ids(rows[1].done)).toEqual(['done']);
    expect(rows[1].open.every((c) => !c.isNew)).toBe(true);
  });

  it('reads a scrum saved before the table in the default order, with leads not recorded', () => {
    const rows = savedScrumRows(
      scrum({
        participants: [
          { userId: 'i', name: 'Idle', counts: noCounts },
          {
            userId: 'w',
            name: 'Worker',
            counts: {
              pending: 1,
              ongoing: 0,
              blocked: 1,
              overdue: 0,
              completed: 0,
            } as TaskCounts,
          },
        ],
        entries: [
          entry('w', 'undated'),
          entry('w', 'stuck', {
            status: 'blocked',
            dueDate: '2026-11-03T00:00:00Z',
          }),
          entry('w', 'theirs', { status: 'in_review', addedTo: true }),
        ],
      })
    );
    expect(names(rows)).toEqual(['Worker', 'Idle']);
    expect(rows[0].leads).toBeNull();
    expect(rows[0].counts.addedTo).toBe(0);
    expect(ids(rows[0].open)).toEqual(['stuck', 'undated', 'theirs']);
    expect(rows[1].open).toEqual([]);
  });

  it('copes with a scrum of nobody', () => {
    expect(savedScrumRows(scrum({}))).toEqual([]);
  });
});

describe('moved', () => {
  it('moves one item and leaves the rest in order', () => {
    expect(moved(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd']);
    expect(moved(['a', 'b', 'c', 'd'], 3, 0)).toEqual(['d', 'a', 'b', 'c']);
  });

  it('changes nothing for a place that is not there', () => {
    expect(moved(['a', 'b'], 0, 5)).toEqual(['a', 'b']);
  });
});

describe('cellStatus', () => {
  it('calls blocked a danger, and names the rest the way the dashboard does', () => {
    expect(cellStatus({ status: 'blocked' })).toEqual({
      label: 'Blocked',
      danger: true,
    });
    expect(cellStatus({ status: 'in_progress' })).toEqual({
      label: 'Ongoing',
      danger: false,
    });
    expect(cellStatus({ status: 'in_review' }).label).toBe('In review');
    expect(cellStatus({ status: 'todo' }).label).toBe('Pending');
  });
});
