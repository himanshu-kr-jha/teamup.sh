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

const ORG = {
  dark: '/api/org/call-backgrounds/dark?v=1',
  light: '',
  pictures: [{ id: 'p1', name: 'Harbour', url: '/api/org/call-pictures/p1?v=a', thumbUrl: '/api/org/call-pictures/p1/thumb?v=a' }],
};

describe('background choice', () => {
  beforeEach(() => localStorage.clear());

  it('is none until something is chosen, and remembered once it is', async () => {
    let m = await load();
    expect(m.processorOptions({ kind: 'none' })).toBeNull();
    m.setBackgroundChoice({ kind: 'picture', id: 'p1' });
    m = await load();
    expect(m.currentBackgroundChoice()).toEqual({ kind: 'picture', id: 'p1' });
    // None is remembered by keeping nothing.
    m.setBackgroundChoice({ kind: 'none' });
    expect(localStorage.getItem('teamup.call.background')).toBeNull();
  });

  it('forgets a stored choice that is no longer on offer, and the picture of your own from before, quietly', async () => {
    for (const stored of [
      // A picture that came with TeamUp, and one of your own, from before only the organization's were offered.
      '{"kind":"image","id":"ocean"}',
      '{"kind":"custom"}',
      '{"kind":"blur","strength":"huge"}',
      '{"kind":"picture"}',
      '{"kind":"company","theme":"sepia"}',
      'not json',
    ]) {
      localStorage.setItem('teamup.call.background', stored);
      localStorage.setItem('teamup.call.background.custom', 'data:image/jpeg;base64,AAAA');
      const m = await load();
      expect(m.currentBackgroundChoice(), stored).toEqual({ kind: 'none' });
      expect(localStorage.getItem('teamup.call.background'), stored).toBeNull();
      expect(localStorage.getItem('teamup.call.background.custom'), stored).toBeNull();
    }
  });

  it('tells the processor a blur by strength, and a picture by its address once it is known', async () => {
    const m = await load();
    expect(m.processorOptions({ kind: 'blur', strength: 'light' })).toEqual({ mode: 'background-blur', blurRadius: 6 });
    expect(m.processorOptions({ kind: 'blur', strength: 'strong' })).toEqual({ mode: 'background-blur', blurRadius: 16 });
    expect(m.processorOptions({ kind: 'picture', id: 'p1' })).toBeNull();
    expect(m.processorOptions({ kind: 'picture', id: 'p1' }, ORG.pictures[0].url)).toEqual({
      mode: 'virtual-background',
      imagePath: '/api/org/call-pictures/p1?v=a',
    });
  });

  it('keeps a company background, and gives the processor the picture made with your name, once there is one', async () => {
    let m = await load();
    m.setBackgroundChoice({ kind: 'company', theme: 'dark' });
    m = await load();
    expect(m.currentBackgroundChoice()).toEqual({ kind: 'company', theme: 'dark' });
    expect(m.processorOptions({ kind: 'company', theme: 'dark' })).toBeNull();
    expect(m.processorOptions({ kind: 'company', theme: 'dark' }, 'data:image/jpeg;base64,BBBB')).toEqual({
      mode: 'virtual-background',
      imagePath: 'data:image/jpeg;base64,BBBB',
    });
  });

  it('says a picture or template the organization has taken away is no longer allowed', async () => {
    const m = await load();
    expect(m.stillAllowed({ kind: 'none' }, ORG)).toBe(true);
    expect(m.stillAllowed({ kind: 'blur', strength: 'strong' }, ORG)).toBe(true);
    expect(m.stillAllowed({ kind: 'picture', id: 'p1' }, ORG)).toBe(true);
    expect(m.stillAllowed({ kind: 'picture', id: 'removed' }, ORG)).toBe(false);
    expect(m.stillAllowed({ kind: 'company', theme: 'dark' }, ORG)).toBe(true);
    expect(m.stillAllowed({ kind: 'company', theme: 'light' }, ORG)).toBe(false);
    // An older server says nothing of pictures: there are none.
    expect(m.stillAllowed({ kind: 'picture', id: 'p1' }, { dark: '', light: '' })).toBe(false);
  });
});
