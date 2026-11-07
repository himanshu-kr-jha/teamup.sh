import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { formatDate } from '@/shared/utils/cn';
import { dueLabel, endOfDueDay, taskDateLabel } from './dueDate';

const now = new Date(2026, 10, 6, 15, 0, 0);

describe('dueLabel', () => {
  it('says nothing about a task with no due date', () => {
    expect(dueLabel({ status: 'todo' }, now)).toBeNull();
    expect(dueLabel({ status: 'todo', dueDate: null }, now)).toBeNull();
  });

  it('names a future date rather than reading "just now"', () => {
    const label = dueLabel({ status: 'todo', dueDate: new Date(2026, 10, 13).toISOString() }, now);
    expect(label).toEqual({ text: 'Due Nov 13', overdue: false });
  });

  it('marks a past date as overdue rather than "3d ago"', () => {
    const label = dueLabel({ status: 'in_progress', dueDate: new Date(2026, 10, 3).toISOString() }, now);
    expect(label).toEqual({ text: 'Overdue Nov 3', overdue: true });
  });

  it('never calls finished work overdue', () => {
    const label = dueLabel({ status: 'done', dueDate: new Date(2026, 10, 3).toISOString() }, now);
    expect(label).toEqual({ text: 'Due Nov 3', overdue: false });
  });

  it('adds the year only when it is not this year', () => {
    const label = dueLabel({ status: 'todo', dueDate: new Date(2027, 0, 2).toISOString() }, now);
    expect(label?.text).toBe('Due Jan 2, 2027');
  });

  it('trusts the flag the server sends over the clock', () => {
    // A saved scrum is frozen: what was on time then stays on time when read later.
    const past = new Date(2026, 10, 3).toISOString();
    expect(dueLabel({ status: 'todo', dueDate: past, overdue: false }, now)?.overdue).toBe(false);
    const future = new Date(2026, 10, 9).toISOString();
    expect(dueLabel({ status: 'todo', dueDate: future, overdue: true }, now)).toEqual({
      text: 'Overdue Nov 9',
      overdue: true,
    });
  });
});

describe('taskDateLabel', () => {
  it('dates finished work by when it finished', () => {
    const label = taskDateLabel(
      { status: 'done', dueDate: new Date(2026, 10, 1).toISOString(), completedAt: new Date(2026, 10, 4).toISOString() },
      now
    );
    expect(label).toEqual({ text: 'Done Nov 4', overdue: false });
  });

  it('falls back to the due date for anything else', () => {
    const label = taskDateLabel({ status: 'blocked', dueDate: new Date(2026, 10, 9).toISOString(), completedAt: null }, now);
    expect(label).toEqual({ text: 'Due Nov 9', overdue: false });
  });

  it('is empty when there is no date at all', () => {
    expect(taskDateLabel({ status: 'todo', dueDate: null, completedAt: null }, now)).toBeNull();
  });
});

describe('endOfDueDay', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  // A zone behind UTC, one ahead with a half-hour offset, and one far ahead: the
  // zones where the UTC date of the instant differs from the day that was picked.
  describe.each(['America/Los_Angeles', 'Asia/Kolkata', 'Pacific/Auckland', 'UTC'])('in %s', (zone) => {
    beforeEach(() => {
      vi.stubEnv('TZ', zone);
    });

    it('is the last millisecond of that local day', () => {
      const end = new Date(endOfDueDay('2026-11-06'));
      expect([end.getFullYear(), end.getMonth(), end.getDate()]).toEqual([2026, 10, 6]);
      expect([end.getHours(), end.getMinutes(), end.getSeconds(), end.getMilliseconds()]).toEqual([23, 59, 59, 999]);
    });

    it('still shows the day that was picked', () => {
      const iso = endOfDueDay('2026-11-06');
      expect(formatDate(iso)).toBe('Nov 6, 2026');
      expect(dueLabel({ status: 'todo', dueDate: iso }, new Date(2026, 10, 6, 12))?.text).toBe('Due Nov 6');
    });

    it('is not overdue until that day is over', () => {
      const iso = endOfDueDay('2026-11-06');
      expect(dueLabel({ status: 'todo', dueDate: iso }, new Date(2026, 10, 6, 0, 0, 1))?.overdue).toBe(false);
      expect(dueLabel({ status: 'todo', dueDate: iso }, new Date(2026, 10, 6, 23, 59, 59))?.overdue).toBe(false);
      expect(dueLabel({ status: 'todo', dueDate: iso }, new Date(2026, 10, 7, 0, 0, 0))?.overdue).toBe(true);
    });
  });

  it('really does run in the zones above', () => {
    vi.stubEnv('TZ', 'Asia/Kolkata');
    expect(new Date(2026, 10, 6).getTimezoneOffset()).toBe(-330);
    vi.stubEnv('TZ', 'America/Los_Angeles');
    expect(new Date(2026, 10, 6).getTimezoneOffset()).toBe(480);
  });

  it('holds on the day the clocks change', () => {
    vi.stubEnv('TZ', 'America/Los_Angeles');
    const end = new Date(endOfDueDay('2026-03-08'));
    expect([end.getDate(), end.getHours(), end.getMinutes()]).toEqual([8, 23, 59]);
  });
});
