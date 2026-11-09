import { beforeEach, describe, expect, it, vi } from 'vitest';

// Node has no localStorage; a map stands in for this browser's.
const store = new Map<string, string>();
vi.stubGlobal('localStorage', {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
  clear: () => store.clear(),
});

// A fresh module each time: the choice is read from storage once, on first use.
async function load() {
  vi.resetModules();
  return import('./background');
}

describe('background choice', () => {
  beforeEach(() => localStorage.clear());

  it('is none until something is chosen, and remembered once it is', async () => {
    let m = await load();
    expect(m.processorOptions({ kind: 'none' })).toBeNull();
    m.setBackgroundChoice({ kind: 'image', id: 'ocean' });
    m = await load();
    expect(m.currentBackgroundChoice()).toEqual({ kind: 'image', id: 'ocean' });
  });

  it('forgets a stored choice that no longer exists, rather than failing', async () => {
    for (const stored of ['{"kind":"image","id":"gone"}', '{"kind":"blur","strength":"huge"}', '{"kind":"custom"}', 'not json']) {
      localStorage.setItem('teamup.call.background', stored);
      const m = await load();
      expect(m.currentBackgroundChoice(), stored).toEqual({ kind: 'none' });
    }
  });

  it('tells the processor a blur by strength, and a picture by its address', async () => {
    const m = await load();
    expect(m.processorOptions({ kind: 'blur', strength: 'light' })).toEqual({ mode: 'background-blur', blurRadius: 6 });
    expect(m.processorOptions({ kind: 'blur', strength: 'strong' })).toEqual({ mode: 'background-blur', blurRadius: 16 });
    expect(m.processorOptions({ kind: 'image', id: 'night' })).toEqual({ mode: 'virtual-background', imagePath: '/backgrounds/night.jpg' });
    // Your own picture, only once there is one.
    expect(m.processorOptions({ kind: 'custom' })).toBeNull();
    localStorage.setItem('teamup.call.background.custom', 'data:image/jpeg;base64,AAAA');
    expect(m.processorOptions({ kind: 'custom' })).toEqual({ mode: 'virtual-background', imagePath: 'data:image/jpeg;base64,AAAA' });
  });

  it('refuses a file that is not a picture', async () => {
    const m = await load();
    await expect(m.saveCustomPicture(new File(['x'], 'notes.txt', { type: 'text/plain' }))).rejects.toThrow(/Choose a picture/);
  });
});
