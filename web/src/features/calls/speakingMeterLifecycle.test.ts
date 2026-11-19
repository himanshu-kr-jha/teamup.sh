import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/** Just enough of an AudioContext to count what is opened, closed and listened for. */
class FakeContext {
  static made: FakeContext[] = [];
  state: 'running' | 'suspended' | 'closed' = 'running';
  sampleRate = 48_000;
  closed = false;
  constructor() {
    FakeContext.made.push(this);
  }
  createAnalyser() {
    return { context: this, fftSize: 256, frequencyBinCount: 128, getByteFrequencyData: () => {} };
  }
  createMediaStreamSource() {
    return { connect: () => {}, disconnect: () => {} };
  }
  resume() {
    return Promise.resolve();
  }
  close() {
    this.closed = true;
    this.state = 'closed';
    return Promise.resolve();
  }
}

const listeners = new Map<string, number>();
const fakeWindow = {
  AudioContext: FakeContext,
  addEventListener: (type: string) => listeners.set(type, (listeners.get(type) ?? 0) + 1),
  removeEventListener: (type: string) => listeners.set(type, (listeners.get(type) ?? 0) - 1),
};

const track = (id: string) => ({ id, readyState: 'live' }) as unknown as MediaStreamTrack;

describe('the shared audio context behind the speaking bars', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.resetModules();
    FakeContext.made = [];
    listeners.clear();
    vi.stubGlobal('window', fakeWindow);
    vi.stubGlobal('MediaStream', class {});
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('is shared while anybody shows a microphone, and closed, listeners and all, once nobody does', async () => {
    const { meterFor, audioOpen, CLOSE_AFTER_MS } = await import('./speakingMeter');
    const one = meterFor(track('a'))!;
    const two = meterFor(track('b'))!;
    const again = meterFor(track('a'))!;
    expect(FakeContext.made).toHaveLength(1);
    expect(listeners.get('pointerdown')).toBe(1);

    one.release();
    two.release();
    vi.advanceTimersByTime(CLOSE_AFTER_MS * 2);
    expect(audioOpen()).toBe(true);

    again.release();
    // A tile moving between layouts does not close it at once.
    vi.advanceTimersByTime(CLOSE_AFTER_MS - 1);
    expect(audioOpen()).toBe(true);
    vi.advanceTimersByTime(1);
    expect(audioOpen()).toBe(false);
    expect(FakeContext.made[0].closed).toBe(true);
    expect(listeners.get('pointerdown')).toBe(0);
    expect(listeners.get('keydown')).toBe(0);
  });

  it('opens a new one for the next call', async () => {
    const { meterFor, audioOpen, CLOSE_AFTER_MS } = await import('./speakingMeter');
    meterFor(track('a'))!.release();
    vi.advanceTimersByTime(CLOSE_AFTER_MS);
    expect(audioOpen()).toBe(false);

    const next = meterFor(track('a'))!;
    expect(FakeContext.made).toHaveLength(2);
    expect(audioOpen()).toBe(true);
    expect(next.read()).not.toBeNull();
  });

  it('stays open when a microphone comes back before it would close', async () => {
    const { meterFor, audioOpen, CLOSE_AFTER_MS } = await import('./speakingMeter');
    meterFor(track('a'))!.release();
    vi.advanceTimersByTime(CLOSE_AFTER_MS / 2);
    const back = meterFor(track('a'))!;
    vi.advanceTimersByTime(CLOSE_AFTER_MS * 2);
    expect(audioOpen()).toBe(true);
    expect(FakeContext.made).toHaveLength(1);
    back.release();
  });
});
