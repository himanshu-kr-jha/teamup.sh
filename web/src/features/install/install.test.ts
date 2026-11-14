import { afterEach, describe, expect, it, vi } from 'vitest';
import { isPhoneApp, isStandalone } from './install';

describe('the phone app', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('counts as installed, so it is never offered an install', () => {
    vi.stubGlobal('navigator', { userAgent: 'Mozilla/5.0 (Linux; Android 15; wv) Chrome/141.0 Mobile Safari/537.36 TeamUpApp' });
    expect(isPhoneApp()).toBe(true);
    expect(isStandalone()).toBe(true);
  });

  it('is not a browser tab on a phone', () => {
    vi.stubGlobal('navigator', { userAgent: 'Mozilla/5.0 (Linux; Android 15) Chrome/141.0 Mobile Safari/537.36' });
    expect(isPhoneApp()).toBe(false);
  });
});
