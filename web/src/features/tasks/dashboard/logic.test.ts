import { describe, expect, it } from 'vitest';
import type { DashboardPerson, Scrum, TaskCounts, TaskSummary } from '@/shared/types/dashboard';
import {
  bucketSections,
  completedCaption,
  defaultRange,
  groupSnapshot,
  hiddenSides,
  nextBefore,
  notesByteLength,
  orderContributions,
  orderPeople,
  orderTasks,
  personForScrum,
  rangeProblem,
  rangeToApi,
  readRange,
  resolveTab,
  scrumLabel,
  tabsForScope,
} from './logic';

const noCounts: TaskCounts = { pending: 0, ongoing: 0, blocked: 0, overdue: 0, completed: 0, addedTo: 0 };

function task(over: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: over.id ?? over.title ?? 't',
    title: 'Task',
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
    counts: noCounts,
    tasks: { pending: [], ongoing: [], blocked: [], overdue: [], completed: [], addedTo: [] },
    newSinceLastScrum: [],
    ...over,
  };
}

describe('tabs', () => {
  it('gives somebody with only their own work two tabs', () => {
    expect(tabsForScope('self')).toEqual(['overview', 'board']);
  });

  it('gives a manager and an admin all six, in the same order', () => {
    const all = ['overview', 'person', 'scrum', 'history', 'contribution', 'board'];
    expect(tabsForScope('team')).toEqual(all);
    expect(tabsForScope('org')).toEqual(all);
  });

  it('opens on Overview', () => {
    expect(resolveTab(null, 'team')).toBe('overview');
    expect(resolveTab('nonsense', 'team')).toBe('overview');
  });

  it('keeps a deep link', () => {
    expect(resolveTab('history', 'org')).toBe('history');
    expect(resolveTab('board', 'self')).toBe('board');
  });

  it('turns an employee away from a manager tab', () => {
    expect(resolveTab('scrum', 'self')).toBe('overview');
  });

  it('does not bounce a deep link before the server has said who this is', () => {
    expect(resolveTab('scrum', undefined)).toBe('scrum');
  });
});

describe('orderTasks', () => {
  it('puts overdue work first, then by priority, then by nearest due date', () => {
    const ordered = orderTasks([
      task({ title: 'low', priority: 'low' }),
      task({ title: 'urgent', priority: 'urgent' }),
      task({ title: 'late', priority: 'low', overdue: true }),
      task({ title: 'high later', priority: 'high', dueDate: '2026-12-01T00:00:00Z' }),
      task({ title: 'high sooner', priority: 'high', dueDate: '2026-11-10T00:00:00Z' }),
      task({ title: 'high undated', priority: 'high' }),
    ]);
    expect(ordered.map((t) => t.title)).toEqual([
      'late',
      'urgent',
      'high sooner',
      'high later',
      'high undated',
      'low',
    ]);
  });

  it('does not change the list it was given', () => {
    const given = [task({ title: 'b', priority: 'low' }), task({ title: 'a', priority: 'urgent' })];
    orderTasks(given);
    expect(given[0].title).toBe('b');
  });
});

describe('orderPeople', () => {
  it('brings anybody blocked up first, leaves people with no work last, and is alphabetical otherwise', () => {
    const ordered = orderPeople([
      person({ name: 'Zed', counts: { ...noCounts, ongoing: 1 } }),
      person({ name: 'Amy' }),
      person({ name: 'Yara', counts: { ...noCounts, blocked: 1 } }),
      person({ name: 'bob', counts: { ...noCounts, completed: 2 } }),
      person({ name: 'Bea', counts: { ...noCounts, blocked: 2 } }),
    ]);
    expect(ordered.map((p) => p.name)).toEqual(['Bea', 'Yara', 'bob', 'Zed', 'Amy']);
  });
});

describe('bucketSections', () => {
  it('uses the spec headings, in order, and leaves out empty ones', () => {
    const sections = bucketSections({
      pending: [task({ title: 'p' })],
      ongoing: [],
      blocked: [task({ title: 'b', status: 'blocked' })],
      overdue: [task({ title: 'o', overdue: true })],
      completed: [task({ title: 'c', status: 'done' })],
      addedTo: [],
    });
    expect(sections.map((s) => s.label)).toEqual(['Pending', 'Blocked', 'Overdue', 'Completed']);
  });

  it('shows the latest finished work first', () => {
    const [completed] = bucketSections({
      pending: [],
      ongoing: [],
      blocked: [],
      overdue: [],
      addedTo: [],
      completed: [
        task({ title: 'older', completedAt: '2026-11-01T00:00:00Z' }),
        task({ title: 'newer', completedAt: '2026-11-05T00:00:00Z' }),
      ],
    });
    expect(completed.tasks.map((t) => t.title)).toEqual(['newer', 'older']);
  });
});

describe('personForScrum', () => {
  it('reads blocked, ongoing, pending, done, and leaves out overdue as its own heading', () => {
    const row = personForScrum(
      person({
        tasks: {
          pending: [task({ title: 'p' })],
          ongoing: [task({ title: 'g', status: 'in_progress' })],
          blocked: [task({ title: 'b', status: 'blocked' })],
          overdue: [task({ title: 'b', status: 'blocked', overdue: true })],
          completed: [task({ title: 'd', status: 'done' })],
          addedTo: [],
        },
      })
    );
    expect(row.sections.map((s) => s.label)).toEqual(['Blocked', 'Ongoing', 'Pending', 'Done']);
  });

  it('flags tasks new since the last scrum', () => {
    const row = personForScrum(
      person({
        tasks: { pending: [task({ id: 'fresh' }), task({ id: 'old' })], ongoing: [], blocked: [], overdue: [], completed: [], addedTo: [] },
        newSinceLastScrum: ['fresh'],
      })
    );
    const flags = Object.fromEntries(row.sections[0].tasks.map((t) => [t.id, t.isNew]));
    expect(flags).toEqual({ fresh: true, old: false });
  });

  it('still produces a row for somebody with no work', () => {
    expect(personForScrum(person({ name: 'Idle' })).sections).toEqual([]);
  });
});

describe('groupSnapshot', () => {
  const entry = (userId: string, taskId: string, status: string) => ({
    userId,
    taskId,
    title: taskId,
    status,
    priority: 'medium',
    overdue: false,
    dueDate: null,
    completedAt: null,
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

  it('files every entry under its person and status', () => {
    const rows = groupSnapshot(
      scrum({
        participants: [
          { userId: 'a', name: 'Ann', counts: { ...noCounts, pending: 1, ongoing: 2, completed: 1 } },
          { userId: 'b', name: 'Ben', counts: { ...noCounts, blocked: 1 } },
        ],
        entries: [
          entry('a', 'a1', 'todo'),
          entry('a', 'a2', 'in_progress'),
          entry('a', 'a3', 'in_review'),
          entry('a', 'a4', 'done'),
          entry('b', 'b1', 'blocked'),
        ],
      })
    );
    const ben = rows.find((r) => r.userId === 'b')!;
    const ann = rows.find((r) => r.userId === 'a')!;
    expect(ben.sections.map((s) => [s.label, s.tasks.map((t) => t.id)])).toEqual([['Blocked', ['b1']]]);
    expect(ann.sections.map((s) => s.label)).toEqual(['Ongoing', 'Pending', 'Done']);
    expect(ann.sections[0].tasks.map((t) => t.id).sort()).toEqual(['a2', 'a3']);
  });

  it('keeps a participant who had no work, and orders people as the live view does', () => {
    const rows = groupSnapshot(
      scrum({
        participants: [
          { userId: 'i', name: 'Idle', counts: noCounts },
          { userId: 'w', name: 'Worker', counts: { ...noCounts, blocked: 1 } },
        ],
        entries: [entry('w', 'w1', 'blocked')],
      })
    );
    expect(rows.map((r) => r.name)).toEqual(['Worker', 'Idle']);
    expect(rows[1].sections).toEqual([]);
  });

  it('marks nothing as new, because a saved scrum has no such idea', () => {
    const [row] = groupSnapshot(
      scrum({
        participants: [{ userId: 'a', name: 'Ann', counts: { ...noCounts, pending: 1 } }],
        entries: [entry('a', 'a1', 'todo')],
      })
    );
    expect(row.sections[0].tasks[0].isNew).toBe(false);
  });

  it('copes with a scrum of nobody', () => {
    expect(groupSnapshot(scrum({}))).toEqual([]);
  });
});

describe('date ranges', () => {
  const now = new Date(2026, 10, 6, 15, 30);

  it('defaults to the last thirty days', () => {
    expect(defaultRange(now)).toEqual({ from: '2026-10-07', to: '2026-11-06' });
  });

  it('reads a valid range from the address', () => {
    expect(readRange('2026-01-01', '2026-02-01', now)).toEqual({ from: '2026-01-01', to: '2026-02-01' });
  });

  it('falls back on anything that is not a date', () => {
    expect(readRange('soon', null, now)).toEqual(defaultRange(now));
    expect(readRange('2026-13-45', '2026-02-01', now).from).toBe('2026-10-07');
  });

  it('asks the server for whole local days', () => {
    const api = rangeToApi({ from: '2026-10-07', to: '2026-11-06' });
    expect(new Date(api.from).getTime()).toBe(new Date(2026, 9, 7, 0, 0, 0, 0).getTime());
    expect(new Date(api.to).getTime()).toBe(new Date(2026, 10, 6, 23, 59, 59, 999).getTime());
  });

  it('objects to a range that runs backwards, and only that', () => {
    expect(rangeProblem({ from: '2026-11-07', to: '2026-11-06' })).toMatch(/before/i);
    expect(rangeProblem({ from: '2026-11-06', to: '2026-11-06' })).toBeNull();
  });
});

describe('orderContributions', () => {
  it('sorts by completed, then involved, then name', () => {
    const row = (name: string, completed: number, involved: number) => ({
      userId: name,
      name,
      teams: [],
      completed,
      involved,
      open: 0,
      addedTo: 0,
    });
    const ordered = orderContributions([row('Cy', 1, 1), row('Ann', 1, 1), row('Bo', 1, 5), row('Di', 3, 0)]);
    expect(ordered.map((p) => p.name)).toEqual(['Di', 'Bo', 'Ann', 'Cy']);
  });
});

describe('nextBefore', () => {
  const page = (n: number) =>
    Array.from({ length: n }, (_, i) => ({ createdAt: `2026-11-0${i + 1}T00:00:00Z` }));

  it('continues from the last item of a full page', () => {
    expect(nextBefore(page(2), 2)).toBe('2026-11-02T00:00:00Z');
  });

  it('stops at a short or empty page', () => {
    expect(nextBefore(page(1), 2)).toBeUndefined();
    expect(nextBefore([], 2)).toBeUndefined();
  });
});

describe('notesByteLength', () => {
  it('counts bytes, because that is what the server limits', () => {
    expect(notesByteLength('abc')).toBe(3);
    expect(notesByteLength('é')).toBe(2);
    expect(notesByteLength('नमस्ते')).toBe(18);
  });
});

describe('scrumLabel', () => {
  it('names the team, or says who it covered', () => {
    expect(scrumLabel({ teamName: 'Platform', scope: 'team' })).toBe('Platform');
    expect(scrumLabel({ teamName: null, scope: 'org' })).toBe('Whole organization');
    expect(scrumLabel({ teamName: null, scope: 'team' })).toBe('All their teams');
  });
});

describe('completedCaption', () => {
  it('says what Completed is counting', () => {
    expect(completedCaption('self', null)).toBe('Completed counts the last 7 days.');
    expect(completedCaption('team', null)).toBe('Completed counts the last 7 days, as no scrum has been saved yet.');
    expect(completedCaption('org', new Date(2026, 10, 3).toISOString())).toBe(
      'Completed counts work since the last scrum on Nov 3, 2026.'
    );
  });
});

describe('hiddenSides', () => {
  it('says nothing when everything fits', () => {
    expect(hiddenSides(0, 400, 400)).toEqual({ left: false, right: false });
  });

  it('marks the right at the start, both in the middle, the left at the end', () => {
    expect(hiddenSides(0, 300, 500)).toEqual({ left: false, right: true });
    expect(hiddenSides(100, 300, 500)).toEqual({ left: true, right: true });
    expect(hiddenSides(200, 300, 500)).toEqual({ left: true, right: false });
  });

  it('forgives the sub-pixel rounding of a zoomed page', () => {
    expect(hiddenSides(0.5, 300, 301)).toEqual({ left: false, right: false });
    expect(hiddenSides(199.5, 300, 500)).toEqual({ left: true, right: false });
  });
});

describe('added to', () => {
  const empty = { pending: [], ongoing: [], blocked: [], overdue: [], completed: [], addedTo: [] };

  it('is its own heading in a list of tasks, apart from their own work', () => {
    const sections = bucketSections({ ...empty, pending: [task({ title: 'Mine' })], addedTo: [task({ title: 'Theirs' })] });
    expect(sections.map((s) => s.label)).toEqual(['Pending', 'Added to']);
  });

  it('copes with a dashboard from before the bucket existed', () => {
    const old = { pending: [], ongoing: [], blocked: [], overdue: [], completed: [], addedTo: [] } as any;
    expect(bucketSections(old)).toEqual([]);
  });

  it('counts as work when ordering people', () => {
    const ordered = orderPeople([
      person({ name: 'Idle' }),
      person({ name: 'Zed', counts: { ...noCounts, addedTo: 1 } }),
    ]);
    expect(ordered.map((p) => p.name)).toEqual(['Zed', 'Idle']);
  });

  it('gets a section in a live scrum', () => {
    const row = personForScrum(person({ tasks: { ...empty, addedTo: [task({ title: 'Theirs' })] } }));
    expect(row.sections.map((s) => s.label)).toEqual(['Added to']);
    expect(row.sections[0].tasks[0].title).toBe('Theirs');
  });

  it('puts a saved added-to entry under Added to whatever its status', () => {
    const entry = { userId: 'a', title: 'T', priority: 'medium', overdue: false, dueDate: null, completedAt: null };
    const rows = groupSnapshot({
      participants: [{ userId: 'a', name: 'Ann', counts: { ...noCounts, pending: 1, addedTo: 1 } }],
      entries: [
        { ...entry, taskId: '1', status: 'todo', addedTo: false },
        { ...entry, taskId: '2', status: 'in_progress', addedTo: true },
      ],
    });
    expect(rows[0].sections.map((s) => [s.label, s.tasks.length])).toEqual([
      ['Pending', 1],
      ['Added to', 1],
    ]);
  });

  it('reads a scrum saved before added-to existed as no added-to, not as broken', () => {
    const entry = { userId: 'a', taskId: '1', title: 'T', status: 'todo', priority: 'medium', overdue: false, dueDate: null, completedAt: null };
    const rows = groupSnapshot({
      participants: [{ userId: 'a', name: 'Ann', counts: { pending: 1, ongoing: 0, blocked: 0, overdue: 0, completed: 0 } as TaskCounts }],
      entries: [entry],
    });
    expect(rows[0].counts.addedTo).toBe(0);
    expect(rows[0].sections.map((s) => s.label)).toEqual(['Pending']);
  });
});
