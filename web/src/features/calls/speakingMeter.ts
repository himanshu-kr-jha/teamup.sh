/**
 * How loud each person is, for the moving bars on their microphone.
 *
 * A call can have a dozen tiles, so nothing here is per tile that does not have to
 * be: one audio context, one analyser per microphone however many places show it,
 * and one animation loop for every set of bars on the screen. The loop writes the
 * bars' heights straight onto them, so a moving bar never re-renders React.
 *
 * Best effort by design. A browser that will not run audio yet (nobody has touched
 * the page) leaves the analyser silent; the bars then follow the level LiveKit
 * reports for each person instead, which is coarser but always there.
 */

/** Loudness of the three bars, low, middle and high, each from 0 to 1. */
export type Bands = [number, number, number];

export const SILENT: Bands = [0, 0, 0];

/**
 * The three bars from a frequency spectrum (getByteFrequencyData, 0-255 per bin).
 * Speech lives between roughly 100 Hz and 4 kHz; the bins are split around
 * where a voice is strongest, and lifted so ordinary talking fills most of a bar.
 */
export function bandsFromSpectrum(spectrum: ArrayLike<number>, sampleRate: number, fftSize: number): Bands {
  const hzPerBin = sampleRate / fftSize;
  const bin = (hz: number) => Math.max(1, Math.min(spectrum.length - 1, Math.round(hz / hzPerBin)));
  const average = (fromHz: number, toHz: number) => {
    const from = bin(fromHz);
    const to = Math.max(from, bin(toHz));
    let sum = 0;
    for (let i = from; i <= to; i++) sum += spectrum[i];
    return sum / (to - from + 1) / 255;
  };
  const lift = (v: number) => Math.min(1, Math.max(0, (v - 0.18) / 0.5)) ** 0.7;
  return [lift(average(100, 500)), lift(average(500, 1500)), lift(average(1500, 4000))];
}

/**
 * The bars from one overall level, for when only LiveKit's figure is known. The
 * middle bar takes the level and the outer ones a share of it that drifts, so a
 * level that is only updated a few times a second still looks like a voice.
 */
export function bandsFromLevel(level: number, now: number): Bands {
  const l = Math.min(1, Math.max(0, level * 2.2)) ** 0.7;
  const drift = (phase: number) => 0.55 + 0.3 * Math.sin(now / 140 + phase);
  return [l * drift(0), l, l * drift(2.1)];
}

/** Moves what is shown toward what is heard: up quickly, down gently, as a meter does. */
export function ease(shown: Bands, heard: Bands): Bands {
  return shown.map((s, i) => {
    const h = heard[i];
    return s + (h - s) * (h > s ? 0.55 : 0.18);
  }) as Bands;
}

// ── one audio context and one analyser per microphone ──────────────────────

let audio: AudioContext | null = null;
let wake: (() => void) | null = null;
let closing: ReturnType<typeof setTimeout> | undefined;

/**
 * How long the audio context outlives its last meter. Tiles come and go as the
 * layout changes, and this keeps that from opening and closing it each time;
 * once the call is over it closes, and its listeners go with it.
 */
export const CLOSE_AFTER_MS = 2_000;

function context(): AudioContext | null {
  clearTimeout(closing);
  closing = undefined;
  if (audio) return audio;
  if (typeof window === 'undefined') return null;
  const Ctor = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctor) return null;
  let made: AudioContext;
  try {
    made = new Ctor();
  } catch {
    return null;
  }
  audio = made;
  // Suspended until the page has been touched; any touch will do.
  wake = () => {
    if (made.state === 'suspended') void made.resume().catch(() => {});
  };
  window.addEventListener('pointerdown', wake, { capture: true });
  window.addEventListener('keydown', wake, { capture: true });
  wake();
  return made;
}

/** Closes the audio context once nothing is listening, and stops waiting for a touch. */
function closeWhenUnused() {
  if (analysers.size > 0 || !audio) return;
  clearTimeout(closing);
  closing = setTimeout(() => {
    closing = undefined;
    if (analysers.size > 0 || !audio) return;
    const done = audio;
    audio = null;
    if (wake) {
      window.removeEventListener('pointerdown', wake, { capture: true });
      window.removeEventListener('keydown', wake, { capture: true });
      wake = null;
    }
    void done.close().catch(() => {});
  }, CLOSE_AFTER_MS);
}

/** Whether the shared audio context is open now; for tests. */
export function audioOpen(): boolean {
  return audio !== null;
}

interface Shared {
  analyser: AnalyserNode;
  source: MediaStreamAudioSourceNode;
  spectrum: Uint8Array<ArrayBuffer>;
  users: number;
}

const analysers = new Map<MediaStreamTrack, Shared>();

export interface Meter {
  /** The three bands now, or null if the analyser cannot hear anything yet. */
  read(): Bands | null;
  release(): void;
}

/** An analyser on a microphone, shared with anything else showing the same one. */
export function meterFor(track: MediaStreamTrack): Meter | null {
  const ctx = context();
  if (!ctx) return null;
  let shared = analysers.get(track);
  // An analyser from a context that has since closed is no use.
  if (shared && shared.analyser.context !== ctx) {
    analysers.delete(track);
    shared = undefined;
  }
  if (!shared) {
    try {
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.5;
      analyser.minDecibels = -85;
      analyser.maxDecibels = -20;
      // Not connected onward: this only listens, so nothing is heard twice.
      const source = ctx.createMediaStreamSource(new MediaStream([track]));
      source.connect(analyser);
      shared = { analyser, source, spectrum: new Uint8Array(analyser.frequencyBinCount), users: 0 };
      analysers.set(track, shared);
    } catch {
      closeWhenUnused();
      return null;
    }
  }
  shared.users++;
  const mine = shared;
  let released = false;
  return {
    read() {
      if (released || ctx.state !== 'running' || track.readyState !== 'live') return null;
      mine.analyser.getByteFrequencyData(mine.spectrum);
      return bandsFromSpectrum(mine.spectrum, ctx.sampleRate, mine.analyser.fftSize);
    },
    release() {
      if (released) return;
      released = true;
      mine.users--;
      if (mine.users > 0) return;
      if (analysers.get(track) === mine) analysers.delete(track);
      try {
        mine.source.disconnect();
      } catch {
        // Already gone.
      }
      closeWhenUnused();
    },
  };
}

// ── one animation loop ─────────────────────────────────────────────────────

/** About thirty frames a second is smooth for a bar and half the work of sixty. */
const FRAME_MS = 1000 / 30;

const watchers = new Set<(now: number) => void>();
let frame = 0;
let last = 0;

function tick(now: number) {
  frame = 0;
  if (watchers.size === 0) return;
  if (now - last >= FRAME_MS) {
    last = now;
    watchers.forEach((watch) => watch(now));
  }
  frame = requestAnimationFrame(tick);
}

/** Calls watch on every frame of the shared loop until the returned function is called. */
export function everyFrame(watch: (now: number) => void): () => void {
  watchers.add(watch);
  if (!frame) frame = requestAnimationFrame(tick);
  return () => {
    watchers.delete(watch);
    if (watchers.size === 0 && frame) {
      cancelAnimationFrame(frame);
      frame = 0;
    }
  };
}
