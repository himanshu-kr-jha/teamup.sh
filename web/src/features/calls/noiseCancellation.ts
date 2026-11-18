// Noise cancellation: whether you want it, whether this device can do it, and
// what the microphone is asked for either way. The processor itself (rnnoise.ts)
// is loaded only once it is wanted.
//
// LiveKit's own noise filter (Krisp) works only with LiveKit Cloud, and TeamUp
// runs its own LiveKit, so this is RNNoise, on the device. While it is on, the
// browser's own noise suppression is turned off, so the two do not stack and
// take the edges off your voice; echo cancellation stays on regardless.

import { useSyncExternalStore } from 'react';
import type { AudioCaptureOptions, RoomOptions } from 'livekit-client';

export const UNAVAILABLE_TEXT = "Noise cancellation isn't available on this device, using the basic filter";

// ── Whether you want it ──────────────────────────────────────────────────

const keyFor = (userId: string) => `teamup.call.noiseCancellation.${userId}`;

/** Your choice in this browser, per person; on for somebody who never chose. */
export function readNoiseChoice(userId: string | undefined, storage: Pick<Storage, 'getItem'> | undefined = safeStorage()): boolean {
  if (!userId || !storage) return true;
  try {
    return storage.getItem(keyFor(userId)) !== 'off';
  } catch {
    return true;
  }
}

/** Keeps it for next time. Not kept where storage is refused; it still applies now. */
export function saveNoiseChoice(userId: string | undefined, on: boolean, storage: Pick<Storage, 'setItem'> | undefined = safeStorage()): void {
  if (!userId || !storage) return;
  try {
    storage.setItem(keyFor(userId), on ? 'on' : 'off');
  } catch {
    // Private browsing, or storage full.
  }
}

// One choice for the page, so the screen before a call and the call agree at once.
let chosen: { userId: string | undefined; on: boolean } | null = null;
const choiceListeners = new Set<() => void>();
const choiceFor = (userId: string | undefined) => {
  if (!chosen || chosen.userId !== userId) chosen = { userId, on: readNoiseChoice(userId) };
  return chosen.on;
};

export function setNoiseChoice(userId: string | undefined, on: boolean) {
  chosen = { userId, on };
  saveNoiseChoice(userId, on);
  choiceListeners.forEach((l) => l());
}

export function useNoiseChoice(userId: string | undefined): boolean {
  return useSyncExternalStore(
    (l) => {
      choiceListeners.add(l);
      return () => choiceListeners.delete(l);
    },
    () => choiceFor(userId),
    () => true
  );
}

/** Forgets the page's copy, for tests that change storage underneath it. */
export function resetNoiseChoiceForTests() {
  chosen = null;
}

// ── Whether this device can ──────────────────────────────────────────────

interface Env {
  AudioContext?: unknown;
  AudioWorkletNode?: unknown;
  WebAssembly?: { instantiate?: unknown };
}

/** Whether the browser has what RNNoise needs: Web Audio with worklets, and WebAssembly. */
export function supportsNoiseCancellation(env: Env = globalThis as unknown as Env): boolean {
  return (
    typeof env.AudioContext === 'function' &&
    typeof env.AudioWorkletNode === 'function' &&
    typeof env.WebAssembly === 'object' &&
    env.WebAssembly !== null &&
    typeof env.WebAssembly.instantiate === 'function'
  );
}

/**
 * Where noise cancellation is: 'off' when not wanted, 'starting' while it loads,
 * 'on' once your microphone goes through it, and 'unavailable' once it failed to
 * start on this device. Failing once is remembered for the page, so a device that
 * cannot is not asked again with every microphone.
 */
export type NoiseStatus = 'off' | 'starting' | 'on' | 'unavailable';

let failed = false;
const failListeners = new Set<() => void>();

export function markNoiseUnavailable() {
  if (failed) return;
  failed = true;
  failListeners.forEach((l) => l());
}

/** Whether RNNoise can be tried here: the browser has what it needs and it has not failed. */
export function noiseCancellationPossible(): boolean {
  return !failed && supportsNoiseCancellation();
}

export function useNoiseCancellationPossible(): boolean {
  return useSyncExternalStore(
    (l) => {
      failListeners.add(l);
      return () => failListeners.delete(l);
    },
    noiseCancellationPossible,
    () => false
  );
}

export function resetNoiseFailureForTests() {
  failed = false;
}

/** What is actually done with your microphone, from what you want and what the device can. */
export function noiseStatus(wanted: boolean, possible: boolean, attached: boolean): NoiseStatus {
  if (!wanted) return 'off';
  if (!possible) return 'unavailable';
  return attached ? 'on' : 'starting';
}

// ── What the microphone is asked for ─────────────────────────────────────

/**
 * The capture settings for a microphone: echo cancellation and gain control on as
 * always, and the browser's noise suppression only when RNNoise is not doing it.
 */
export function captureFor(rnnoise: boolean): Pick<AudioCaptureOptions, 'echoCancellation' | 'autoGainControl' | 'noiseSuppression'> {
  return { echoCancellation: true, autoGainControl: true, noiseSuppression: !rnnoise };
}

/** A call's room options, with the microphone captured for the noise cancellation chosen as it starts. */
export function withNoiseCapture(options: RoomOptions, userId: string | undefined): RoomOptions {
  const rnnoise = readNoiseChoice(userId) && noiseCancellationPossible();
  return { ...options, audioCaptureDefaults: { ...options.audioCaptureDefaults, ...captureFor(rnnoise) } };
}

function safeStorage(): Storage | undefined {
  try {
    return typeof localStorage === 'undefined' ? undefined : localStorage;
  } catch {
    return undefined;
  }
}
