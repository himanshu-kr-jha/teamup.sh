import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe('the single-key switch', () => {
  it('is on until turned off, and is kept on the device', async () => {
    const store = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => store.set(k, v),
      removeItem: (k: string) => store.delete(k),
    });
    const { singleKeysOn, setSingleKeys } = await import('./preference');
    expect(singleKeysOn()).toBe(true);
    setSingleKeys(false);
    expect(singleKeysOn()).toBe(false);
    expect([...store.values()]).toEqual(['off']);
    setSingleKeys(true);
    expect(singleKeysOn()).toBe(true);
    expect(store.size).toBe(0);
  });

  it('still works for this tab when storage is refused', async () => {
    const refuse = () => {
      throw new Error('SecurityError');
    };
    vi.stubGlobal('localStorage', { getItem: refuse, setItem: refuse, removeItem: refuse });
    const { singleKeysOn, setSingleKeys } = await import('./preference');
    expect(singleKeysOn()).toBe(true);
    setSingleKeys(false);
    expect(singleKeysOn()).toBe(false);
  });
});
