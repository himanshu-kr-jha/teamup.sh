// What the voice and video players share: the bars of a waveform, the time,
// and the length of a recording that does not say how long it is.

/** How many bars a voice message's waveform has. */
export const BARS = 40;

/** "0:07", "12:30". */
export function clock(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '0:00';
  return `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, '0')}`;
}

/**
 * The loudness of each stretch of a recording, from 0.08 to 1: the root mean
 * square of each bar's samples, scaled so the loudest bar is full height and
 * silence still shows as a sliver.
 */
export function peaksOf(samples: Float32Array, bars = BARS): number[] {
  const out: number[] = [];
  const size = Math.max(1, Math.floor(samples.length / bars));
  for (let b = 0; b < bars; b++) {
    let sum = 0;
    const from = b * size;
    const to = Math.min(samples.length, from + size);
    for (let i = from; i < to; i++) sum += samples[i] * samples[i];
    out.push(to > from ? Math.sqrt(sum / (to - from)) : 0);
  }
  const loudest = Math.max(...out, 1e-6);
  return out.map((v) => Math.max(0.08, Math.min(1, v / loudest)));
}

/**
 * A waveform made up from a name, for when the recording cannot be read for
 * one: the same name always looks the same, and it looks like speech rather
 * than a flat line.
 */
export function madeUpPeaks(seed: string, bars = BARS): number[] {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  const out: number[] = [];
  for (let b = 0; b < bars; b++) {
    h ^= h << 13;
    h ^= h >>> 17;
    h ^= h << 5;
    const noise = ((h >>> 0) % 1000) / 1000;
    // Louder in the middle of a phrase, quieter at its ends.
    const envelope = 0.45 + 0.55 * Math.sin((Math.PI * (b + 0.5)) / bars);
    out.push(Math.max(0.12, Math.min(1, envelope * (0.35 + 0.65 * noise))));
  }
  return out;
}

/**
 * The length of a media element's recording. A recording made in a browser
 * (WebM from MediaRecorder) says its length is Infinity until it has been
 * played through; seeking far past the end makes the browser work it out.
 */
export function whenDurationKnown(el: HTMLMediaElement, done: (seconds: number) => void): () => void {
  let settled = false;
  const settle = () => {
    if (settled || !Number.isFinite(el.duration) || el.duration <= 0) return;
    settled = true;
    done(el.duration);
  };
  const onMetadata = () => {
    if (Number.isFinite(el.duration) && el.duration > 0) return settle();
    const onSeeked = () => {
      el.removeEventListener('timeupdate', onSeeked);
      if (Number.isFinite(el.duration)) {
        el.currentTime = 0;
        settle();
      }
    };
    el.addEventListener('timeupdate', onSeeked);
    el.currentTime = 1e101;
  };
  el.addEventListener('loadedmetadata', onMetadata);
  el.addEventListener('durationchange', settle);
  if (el.readyState >= 1) onMetadata();
  return () => {
    el.removeEventListener('loadedmetadata', onMetadata);
    el.removeEventListener('durationchange', settle);
  };
}

/** The playback speeds a voice message cycles through. */
export const SPEEDS = [1, 1.5, 2] as const;

export function nextSpeed(current: number): number {
  const i = SPEEDS.indexOf(current as (typeof SPEEDS)[number]);
  return SPEEDS[(i + 1) % SPEEDS.length];
}
