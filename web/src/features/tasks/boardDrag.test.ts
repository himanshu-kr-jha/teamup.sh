import { describe, expect, it } from 'vitest';
import { MOVE_BEFORE_OVER, moveOf, statusOfDrop, stillUntilMoved } from './boardDrag';

describe('a held card', () => {
  const start = { x: 100, y: 300 };

  it('is over nothing until the finger moves, whatever slides under it', () => {
    expect(stillUntilMoved(start, start)).toBe(true);
    expect(stillUntilMoved(start, { x: 105, y: 305 })).toBe(true);
    expect(stillUntilMoved(start, { x: 100, y: 300 - MOVE_BEFORE_OVER })).toBe(false);
  });

  it('is not held back on the keyboard, which has no pointer', () => {
    expect(stillUntilMoved(null, null)).toBe(false);
    expect(stillUntilMoved(start, null)).toBe(false);
  });
});

describe('a finished drag', () => {
  const data = { taskId: 't', status: 'todo', title: 'Fix sign-in' };

  it('moves the task to the column it was dropped on', () => {
    expect(statusOfDrop('phone:column:blocked')).toBe('blocked');
    expect(moveOf(data, 'wide:column:in_review')).toEqual({ taskId: 't', from: 'todo', to: 'in_review' });
  });

  it('does nothing when dropped back where it was, or nowhere', () => {
    expect(moveOf(data, 'wide:column:todo')).toBeNull();
    expect(moveOf(data, null)).toBeNull();
    expect(moveOf(data, 'wide:task:x')).toBeNull();
  });
});
