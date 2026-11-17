// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { matchesDue, matchesPriority, matchesSearch, rememberMineAs, rememberedMineAs } from './boardFilters';

// Wednesday 18 November 2026, noon.
const now = new Date(2026, 10, 18, 12, 0);
const task = (over: Partial<{ key: string; title: string; status: string; priority: string; dueDate: string | null }> = {}) => ({
  key: 'CHK-12',
  title: 'Round coupon discounts',
  status: 'todo',
  priority: 'high',
  dueDate: null,
  ...over,
});

describe('matchesSearch', () => {
  it('finds by every word of the title or the key, in any order', () => {
    expect(matchesSearch(task(), 'coupon round')).toBe(true);
    expect(matchesSearch(task(), 'chk-12')).toBe(true);
    expect(matchesSearch(task(), 'coupon refund')).toBe(false);
    expect(matchesSearch(task(), '  ')).toBe(true);
  });
});

describe('matchesDue', () => {
  it('overdue is past its date and not done', () => {
    expect(matchesDue(task({ dueDate: new Date(2026, 10, 17).toISOString() }), 'overdue', now)).toBe(true);
    expect(matchesDue(task({ dueDate: new Date(2026, 10, 17).toISOString(), status: 'done' }), 'overdue', now)).toBe(false);
    expect(matchesDue(task({ dueDate: new Date(2026, 10, 19).toISOString() }), 'overdue', now)).toBe(false);
  });

  it('this week runs to Sunday night', () => {
    expect(matchesDue(task({ dueDate: new Date(2026, 10, 22, 20).toISOString() }), 'week', now)).toBe(true);
    expect(matchesDue(task({ dueDate: new Date(2026, 10, 23, 9).toISOString() }), 'week', now)).toBe(false);
    expect(matchesDue(task({ dueDate: new Date(2026, 10, 17).toISOString() }), 'week', now)).toBe(false);
  });

  it('no due date, and any', () => {
    expect(matchesDue(task(), 'none', now)).toBe(true);
    expect(matchesDue(task({ dueDate: now.toISOString() }), 'none', now)).toBe(false);
    expect(matchesDue(task(), 'any', now)).toBe(true);
  });
});

describe('matchesPriority', () => {
  it('narrows to one priority', () => {
    expect(matchesPriority(task(), 'high')).toBe(true);
    expect(matchesPriority(task(), 'low')).toBe(false);
    expect(matchesPriority(task(), 'any')).toBe(true);
  });
});

describe('remembered choice of my tasks', () => {
  it('is all of them at first, and what was last chosen after', () => {
    localStorage.clear();
    expect(rememberedMineAs('u1')).toBe('all');
    rememberMineAs('u1', 'created');
    expect(rememberedMineAs('u1')).toBe('created');
    expect(rememberedMineAs('u2')).toBe('all');
  });
});
