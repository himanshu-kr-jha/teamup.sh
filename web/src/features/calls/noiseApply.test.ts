import { describe, expect, it } from 'vitest';
import { RNNOISE, applyNoiseCancellation, type NoiseTrack } from './noiseApply';

/**
 * A microphone track as LiveKit's behaves for this: a processor's init opens an
 * audio context and its destroy closes it, and asking for the microphone again
 * is counted.
 */
function fakeTrack(options: { noiseSuppression?: boolean; initDelay?: Promise<void> } = {}) {
  const log = { openContexts: 0, recaptures: [] as boolean[] };
  let processor: { name: string; init: () => Promise<void>; destroy: () => Promise<void> } | undefined;
  const mst = {
    readyState: 'live' as MediaStreamTrackState,
    settings: { deviceId: 'mic-1', noiseSuppression: options.noiseSuppression ?? true },
    getSettings() {
      return this.settings;
    },
  };
  const track = {
    mediaStreamTrack: mst,
    constraints: { deviceId: 'mic-1' },
    getProcessor: () => processor,
    setProcessor: async (p: typeof processor) => {
      await p!.init();
      processor = p;
    },
    stopProcessor: async () => {
      await processor?.destroy();
      processor = undefined;
    },
    restartTrack: async (o: { noiseSuppression?: boolean }) => {
      log.recaptures.push(!!o.noiseSuppression);
      mst.settings = { ...mst.settings, noiseSuppression: !!o.noiseSuppression };
    },
  } as unknown as NoiseTrack;
  const load = async () => ({
    createRnnoiseProcessor: () =>
      ({
        name: RNNOISE,
        init: async () => {
          log.openContexts++;
          await options.initDelay;
        },
        destroy: async () => {
          log.openContexts--;
        },
      }) as never,
  });
  return { track, mst, log, load };
}

const deferred = () => {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => (resolve = r));
  return { promise, resolve };
};

describe('putting noise cancellation on the microphone', () => {
  it('puts it on, with the browser’s own suppression turned off', async () => {
    const { track, log, load } = fakeTrack();
    expect(await applyNoiseCancellation(track, true, () => true, load)).toBe('done');
    expect(track.getProcessor()?.name).toBe(RNNOISE);
    expect(log.recaptures).toEqual([false]);
    expect(log.openContexts).toBe(1);
  });

  it('takes it off again, with the browser’s suppression back on', async () => {
    const { track, log, load } = fakeTrack();
    await applyNoiseCancellation(track, true, () => true, load);
    expect(await applyNoiseCancellation(track, false, () => true, load)).toBe('done');
    expect(track.getProcessor()).toBeUndefined();
    expect(log.recaptures).toEqual([false, true]);
    expect(log.openContexts).toBe(0);
  });

  it('does nothing, and opens no microphone, when the call ends while the processor loads', async () => {
    const { track, log } = fakeTrack();
    let alive = true;
    const gate = deferred();
    const slowLoad = async () => {
      await gate.promise;
      return fakeTrack().load();
    };
    const run = applyNoiseCancellation(track, true, () => alive, slowLoad);
    alive = false;
    gate.resolve();
    expect(await run).toBe('cancelled');
    expect(log.recaptures).toEqual([]);
    expect(track.getProcessor()).toBeUndefined();
  });

  it('closes the audio context it opened when the call ends while it starts', async () => {
    const gate = deferred();
    const { track, mst, log, load } = fakeTrack({ initDelay: gate.promise, noiseSuppression: false });
    const run = applyNoiseCancellation(track, true, () => true, load);
    await new Promise((r) => setTimeout(r, 0));
    expect(log.openContexts).toBe(1);
    // Leaving stops the microphone, as LiveKit does.
    mst.readyState = 'ended';
    gate.resolve();
    expect(await run).toBe('cancelled');
    expect(log.openContexts).toBe(0);
    expect(track.getProcessor()).toBeUndefined();
    expect(log.recaptures).toEqual([]);
  });

  it('does not touch a microphone that has already stopped', async () => {
    const { track, mst, log, load } = fakeTrack();
    mst.readyState = 'ended';
    expect(await applyNoiseCancellation(track, true, () => true, load)).toBe('cancelled');
    expect(await applyNoiseCancellation(track, false, () => true, load)).toBe('cancelled');
    expect(log.recaptures).toEqual([]);
  });

  it('waits for a tap where the page’s audio cannot start yet, using the basic filter meanwhile', async () => {
    const { track, log } = fakeTrack();
    const suspended = async () => ({
      createRnnoiseProcessor: () =>
        ({
          name: RNNOISE,
          init: async () => {
            throw new DOMException('suspended', 'NotAllowedError');
          },
          destroy: async () => undefined,
        }) as never,
    });
    expect(await applyNoiseCancellation(track, true, () => true, suspended)).toBe('suspended');
    expect(track.getProcessor()).toBeUndefined();
    // Off for RNNoise, then back on for the browser's own filter.
    expect(log.recaptures).toEqual([false, true]);
  });

  it('says it failed where the device cannot', async () => {
    const { track } = fakeTrack();
    const broken = async () => {
      throw new Error('no worklet');
    };
    expect(await applyNoiseCancellation(track, true, () => true, broken)).toBe('failed');
  });
});
