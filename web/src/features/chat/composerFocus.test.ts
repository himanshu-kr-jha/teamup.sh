import { describe, expect, it } from 'vitest';
import { typesOnOpen } from './composerFocus';

const screen = (fine: boolean) => ({ matchMedia: (q: string) => ({ matches: q === '(pointer: fine)' && fine }) as MediaQueryList });

describe('typesOnOpen', () => {
  it('puts the cursor in the box where there is a keyboard and a mouse', () => {
    expect(typesOnOpen(screen(true))).toBe(true);
  });

  it('leaves a phone keyboard down until the box is tapped', () => {
    expect(typesOnOpen(screen(false))).toBe(false);
  });

  it('does nothing where it cannot tell', () => {
    expect(typesOnOpen(undefined)).toBe(false);
  });
});
