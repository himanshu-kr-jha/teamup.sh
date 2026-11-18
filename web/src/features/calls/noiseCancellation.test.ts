import { afterEach, describe, expect, it } from 'vitest';
import {
  captureFor,
  markNoiseUnavailable,
  noiseCancellationPossible,
  noiseStatus,
  readNoiseChoice,
  resetNoiseChoiceForTests,
  resetNoiseFailureForTests,
  saveNoiseChoice,
  supportsNoiseCancellation,
  withNoiseCapture,
} from './noiseCancellation';

function memoryStorage() {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
}

const fn = () => undefined;
const fullBrowser = { AudioContext: fn, AudioWorkletNode: fn, WebAssembly: { instantiate: fn } };

afterEach(() => {
  resetNoiseFailureForTests();
  resetNoiseChoiceForTests();
});

describe('the remembered choice', () => {
  it('is on for somebody who never chose', () => {
    expect(readNoiseChoice('u1', memoryStorage())).toBe(true);
    expect(readNoiseChoice(undefined, memoryStorage())).toBe(true);
  });

  it('is kept per person', () => {
    const storage = memoryStorage();
    saveNoiseChoice('u1', false, storage);
    expect(readNoiseChoice('u1', storage)).toBe(false);
    expect(readNoiseChoice('u2', storage)).toBe(true);
    saveNoiseChoice('u1', true, storage);
    expect(readNoiseChoice('u1', storage)).toBe(true);
  });

  it('is on, and nothing breaks, where storage is refused', () => {
    const refuse = () => {
      throw new Error('denied');
    };
    expect(readNoiseChoice('u1', { getItem: refuse })).toBe(true);
    expect(() => saveNoiseChoice('u1', false, { setItem: refuse })).not.toThrow();
  });
});

describe('falling back to the basic filter', () => {
  it('needs AudioWorklet and WebAssembly', () => {
    expect(supportsNoiseCancellation(fullBrowser)).toBe(true);
    expect(supportsNoiseCancellation({ ...fullBrowser, AudioWorkletNode: undefined })).toBe(false);
    expect(supportsNoiseCancellation({ ...fullBrowser, WebAssembly: undefined })).toBe(false);
    expect(supportsNoiseCancellation({ ...fullBrowser, AudioContext: undefined })).toBe(false);
    expect(supportsNoiseCancellation({})).toBe(false);
  });

  it('says unavailable when wanted but not possible, and off when not wanted', () => {
    expect(noiseStatus(true, true, true)).toBe('on');
    expect(noiseStatus(true, true, false)).toBe('starting');
    expect(noiseStatus(true, false, false)).toBe('unavailable');
    expect(noiseStatus(false, false, false)).toBe('off');
    expect(noiseStatus(false, true, false)).toBe('off');
  });

  it('stops trying once it failed to start on this device', () => {
    const before = noiseCancellationPossible();
    markNoiseUnavailable();
    expect(noiseCancellationPossible()).toBe(false);
    resetNoiseFailureForTests();
    expect(noiseCancellationPossible()).toBe(before);
  });

  it("keeps the browser's own noise suppression only while RNNoise is not doing it, and echo cancellation always", () => {
    expect(captureFor(true)).toEqual({ echoCancellation: true, autoGainControl: true, noiseSuppression: false });
    expect(captureFor(false)).toEqual({ echoCancellation: true, autoGainControl: true, noiseSuppression: true });
  });

  it("captures a call's microphone with the browser's suppression where RNNoise cannot run", () => {
    // The test environment has no AudioWorklet, as an old browser does not.
    const options = withNoiseCapture({ adaptiveStream: true, audioCaptureDefaults: { deviceId: 'mic-2' } }, 'u1');
    expect(options.adaptiveStream).toBe(true);
    expect(options.audioCaptureDefaults).toEqual({ deviceId: 'mic-2', echoCancellation: true, autoGainControl: true, noiseSuppression: true });
  });
});
