import { describe, expect, it } from 'vitest';
import { LABEL_COLORS, colorFor, existingLabel } from './labels';

describe('labels', () => {
  it('gives a name the same colour every time, whatever its case', () => {
    expect(colorFor('Frontend')).toBe(colorFor(' frontend '));
    expect(LABEL_COLORS).toContain(colorFor('bug'));
  });

  it('finds a label already made, so typing it again does not make another', () => {
    const all = [{ id: '1', name: 'Bug' }];
    expect(existingLabel(all, ' bug ')?.id).toBe('1');
    expect(existingLabel(all, 'bugs')).toBeUndefined();
  });
});
