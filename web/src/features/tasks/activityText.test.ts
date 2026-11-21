import { describe, expect, it } from 'vitest';
import { activityLine } from './activityText';

const names = (id: string) => (id === 'u1' ? 'Asha' : undefined);

describe('a line of task history', () => {
  it('says a comment was written, not that a field called comment changed', () => {
    expect(activityLine({ field: 'comment', newValue: 'Looks good' }, names)).toEqual({ verb: 'commented', to: 'Looks good' });
  });
  it('says where a task moved, and the key it had', () => {
    expect(activityLine({ field: 'project', oldValue: 'TASK-7', newValue: 'Photon as PHO-31' }, names)).toEqual({
      verb: 'moved it to Photon as PHO-31, was TASK-7',
    });
  });
  it('names people rather than showing their ids', () => {
    expect(activityLine({ field: 'assigneeId', oldValue: 'u1', newValue: 'u2' }, names)).toEqual({
      verb: 'changed the assignee',
      from: 'Asha',
      to: 'somebody',
    });
    expect(activityLine({ field: 'assigneeId', oldValue: 'u1', newValue: '' }, names)).toEqual({ verb: 'unassigned it', from: 'Asha' });
  });
  it('words statuses and dates the way the page does', () => {
    expect(activityLine({ field: 'status', oldValue: 'todo', newValue: 'in_review' }, names)).toEqual({
      verb: 'changed status',
      from: 'To do',
      to: 'In review',
    });
    expect(activityLine({ field: 'dueDate', newValue: '2026-11-13T18:29:59Z' }, names).verb).toBe('changed the due date');
    expect(activityLine({ field: 'dueDate', oldValue: '2026-11-13T18:29:59Z', newValue: '' }, names).verb).toBe('removed the due date');
  });
  it('says what a task was blocked on, when it changed, and when it was cleared', () => {
    expect(activityLine({ field: 'blockedOn', oldValue: '', newValue: 'Waiting for keys' }, names)).toEqual({
      verb: 'said it is blocked on',
      to: 'Waiting for keys',
    });
    expect(activityLine({ field: 'blockedOn', oldValue: 'Waiting for keys', newValue: 'Keys Monday' }, names)).toEqual({
      verb: 'changed what it is blocked on',
      from: 'Waiting for keys',
      to: 'Keys Monday',
    });
    expect(activityLine({ field: 'blockedOn', oldValue: 'Keys Monday', newValue: '' }, names)).toEqual({
      verb: 'cleared what it was blocked on',
      from: 'Keys Monday',
    });
  });
});
