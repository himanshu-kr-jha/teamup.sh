import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProcessorOptions, Track } from 'livekit-client';

// Somebody with a background chosen must never send the room behind them, not
// even the camera's first frame: every camera is opened with NewCameraProcessor
// on it, which LiveKit puts on before publishing. These check what it does with
// each choice, without a real camera or MediaPipe.

const store = new Map<string, string>();
vi.stubGlobal('localStorage', {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
  clear: () => store.clear(),
});

// The processor's own module, which needs a browser: what it was asked for is noted.
const calls = vi.hoisted(() => ({
  made: [] as { background: unknown; look: unknown }[],
  keep: [] as boolean[],
  warmed: 0,
  release: null as null | (() => void),
}));
vi.mock('./cameraProcessor', () => ({
  keepEffects: (on: boolean) => calls.keep.push(on),
  warmEffects: () => void calls.warmed++,
  preloadEffects: () => Promise.resolve(),
  processorForNewCamera: (background: unknown, look: unknown) => {
    calls.made.push({ background, look });
    return {
      name: 'teamup-camera',
      processedTrack: { id: 'processed' },
      init: () => new Promise<void>((resolve) => (calls.release = resolve)),
      restart: async () => undefined,
      destroy: async () => undefined,
    };
  },
}));

async function load() {
  vi.resetModules();
  return import('./background');
}

const camera = { track: { getSettings: () => ({ width: 640, height: 360 }) } } as unknown as ProcessorOptions<Track.Kind.Video>;

describe('the processor every camera is opened with', () => {
  beforeEach(() => {
    store.clear();
    calls.made = [];
    calls.keep = [];
    calls.warmed = 0;
    calls.release = null;
  });

  it('does nothing, and loads nothing, with nothing chosen: the camera goes as it is', async () => {
    const m = await load();
    const p = new m.NewCameraProcessor();
    await p.init(camera);
    expect(p.processedTrack).toBeUndefined();
    expect(p.idle).toBe(true);
    expect(calls.made).toEqual([]);
    m.warmCameraEffects();
    await Promise.resolve();
    expect(calls.warmed).toBe(0);
  });

  it('holds the camera back until the blur is ready, saying so meanwhile, then sends it blurred', async () => {
    let m = await load();
    m.setBackgroundChoice({ kind: 'blur', strength: 'light' });
    m = await load();
    const p = new m.NewCameraProcessor();
    const starting = p.init(camera);
    await vi.waitFor(() => expect(calls.release).not.toBeNull());
    expect(calls.made).toEqual([{ background: { blurRadius: 6 }, look: { filter: '', wash: '' } }]);
    // Kept for the next call, as something is chosen.
    expect(calls.keep).toEqual([true]);
    expect(p.processedTrack).toBeUndefined();
    calls.release!();
    await starting;
    expect(p.processedTrack).toEqual({ id: 'processed' });
  });

  it('starts a picture behind the strong blur, which hides the room as well until the picture is on', async () => {
    let m = await load();
    m.setBackgroundChoice({ kind: 'picture', id: 'p1' });
    m = await load();
    const p = new m.NewCameraProcessor();
    const starting = p.init(camera);
    await vi.waitFor(() => expect(calls.release).not.toBeNull());
    expect(calls.made[0].background).toEqual({ blurRadius: 16 });
    calls.release!();
    await starting;
  });

  it('goes on the camera for a look alone, and is let go of when nothing is chosen any more', async () => {
    let m = await load();
    m.setLook('mono');
    m = await load();
    const p = new m.NewCameraProcessor();
    const starting = p.init(camera);
    await vi.waitFor(() => expect(calls.release).not.toBeNull());
    expect(calls.made[0]).toEqual({ background: null, look: { filter: 'grayscale(1) contrast(1.08)', wash: '' } });
    calls.release!();
    await starting;
    m.setLook('natural');
    await vi.waitFor(() => expect(calls.keep[calls.keep.length - 1]).toBe(false));
  });

  it('counts as no effect while idle, so the picker still shows its own preview', async () => {
    let m = await load();
    const idle = new m.NewCameraProcessor();
    await idle.init(camera);
    // Asked of the module the processor came from, as each load is a fresh one.
    const on = (held: unknown) => m.hasCameraEffects({ getProcessor: () => held } as unknown as import('livekit-client').LocalVideoTrack);
    expect(on(undefined)).toBe(false);
    expect(on(idle)).toBe(false);
    expect(on({ name: 'teamup-camera' })).toBe(true);
    m.setBackgroundChoice({ kind: 'blur', strength: 'strong' });
    m = await load();
    const busy = new m.NewCameraProcessor();
    const starting = busy.init(camera);
    await vi.waitFor(() => expect(calls.release).not.toBeNull());
    calls.release!();
    await starting;
    expect(on(busy)).toBe(true);
  });

  it('is a new one for each room, and keeps the camera the call was joined with', async () => {
    const m = await load();
    const a = m.withCameraEffects({ videoCaptureDefaults: { deviceId: 'cam-2' } });
    const b = m.withCameraEffects({ videoCaptureDefaults: { deviceId: 'cam-2' } });
    expect(a.videoCaptureDefaults?.deviceId).toBe('cam-2');
    expect(a.videoCaptureDefaults?.processor).toBeInstanceOf(m.NewCameraProcessor);
    expect(a.videoCaptureDefaults?.processor).not.toBe(b.videoCaptureDefaults?.processor);
  });
});
