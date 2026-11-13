import { describe, expect, it } from 'vitest';
import config from './vite.config.ts';

describe('the production build', () => {
  it('ships no source maps, which the server would hand to anybody who asked', () => {
    expect(config.build?.sourcemap ?? false).toBe(false);
  });
});
