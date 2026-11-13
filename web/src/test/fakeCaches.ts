import { vi } from 'vitest';

/**
 * A stand-in for Cache Storage, which happy-dom lacks: only which caches exist,
 * since what signing out must do is make them not exist.
 */
export function fakeCaches(names: string[]) {
  const open = new Set(names);
  const storage = {
    delete: vi.fn(async (name: string) => open.delete(name)),
    open: vi.fn(async (name: string) => {
      open.add(name);
      return { match: async () => undefined, put: async () => undefined };
    }),
    has: async (name: string) => open.has(name),
    keys: async () => [...open],
  };
  vi.stubGlobal('caches', storage);
  return { storage, remaining: () => [...open].sort() };
}
