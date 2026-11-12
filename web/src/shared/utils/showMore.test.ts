import { describe, expect, it } from 'vitest';
import { showMoreText, visiblePart } from './showMore';

describe('show more', () => {
  const items = [1, 2, 3, 4, 5, 6, 7];

  it('shows the first few, or the last few, until asked for all', () => {
    expect(visiblePart(items, 3, false)).toEqual({ visible: [1, 2, 3], hidden: 4 });
    expect(visiblePart(items, 3, false, 'end')).toEqual({ visible: [5, 6, 7], hidden: 4 });
    expect(visiblePart(items, 3, true)).toEqual({ visible: items, hidden: 0 });
  });

  it('hides nothing from a short list', () => {
    expect(visiblePart(items, 7, false)).toEqual({ visible: items, hidden: 0 });
  });

  it('says how many more there are', () => {
    expect(showMoreText(4, false, ['change', 'changes'])).toBe('Show 4 more changes');
    expect(showMoreText(1, false, ['comment', 'comments'], true)).toBe('Show 1 earlier comment');
    expect(showMoreText(4, true, ['change', 'changes'])).toBe('Show less');
  });
});
