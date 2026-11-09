// The app's recorded sounds: the ring of an incoming call, the ringback a caller
// hears while it rings, the end of a call, and a new message. Each is fetched and
// decoded once, then played through one shared audio context, so a looping ring
// starts and stops exactly and never overlaps itself.
//
// Best effort by design, as with the synthesised tones in useChime: a browser
// refuses to play sound on a page nobody has touched yet, so whatever plays a
// sound must also say what happened some other way. Nothing here throws.

export type SoundName = 'ringtone' | 'ringback' | 'call-ended' | 'notification';

const FILES: Record<SoundName, string> = {
  ringtone: '/sounds/ringtone.mp3',
  ringback: '/sounds/ringback.mp3',
  'call-ended': '/sounds/call-ended.mp3',
  notification: '/sounds/notification.mp3',
};

/** How loud each one plays: the files are levelled alike, and a ring may be a little louder than a message. */
const VOLUME: Record<SoundName, number> = {
  ringtone: 0.9,
  ringback: 0.6,
  'call-ended': 0.7,
  notification: 0.7,
};

/** A one-off sound that could not start within this long is out of date, and dropped. */
const ONE_OFF_STALE_MS = 2_000;

/** What a test, or anything else, can listen for to learn that a sound started. */
export const SOUND_EVENT = 'teamup:sound';

let ctx: AudioContext | null = null;
const buffers = new Map<SoundName, Promise<AudioBuffer | null>>();

function context(): AudioContext | null {
  if (ctx) return ctx;
  const Ctor = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctor) return null;
  try {
    ctx = new Ctor();
  } catch {
    return null;
  }
  return ctx;
}

function load(name: SoundName): Promise<AudioBuffer | null> {
  let found = buffers.get(name);
  if (!found) {
    found = (async () => {
      const audio = context();
      if (!audio) return null;
      try {
        const res = await fetch(FILES[name]);
        if (!res.ok) return null;
        return await audio.decodeAudioData(await res.arrayBuffer());
      } catch {
        return null;
      }
    })();
    // A failure is not remembered: the next try fetches again.
    found.then((b) => b === null && buffers.delete(name));
    buffers.set(name, found);
  }
  return found;
}

/** Fetches and decodes the sounds ahead of need, so the first ring is not late. */
export function preloadSounds(names: SoundName[] = ['ringtone', 'ringback', 'call-ended', 'notification']) {
  names.forEach((n) => void load(n));
}

/** Stops a sound that is playing, or keeps it from starting. Calling it twice is fine. */
export type StopSound = () => void;

/**
 * Plays a sound, once or until stopped. A sound the browser will not play yet is
 * dropped rather than held: the context would otherwise resume on some later
 * click and play everything queued at once.
 */
export function playSound(name: SoundName, options: { loop?: boolean } = {}): StopSound {
  let stopped = false;
  let source: AudioBufferSourceNode | null = null;
  const stop: StopSound = () => {
    stopped = true;
    try {
      source?.stop();
    } catch {
      // Not started, or already over.
    }
    source = null;
  };

  const audio = context();
  if (!audio) return stop;
  const asked = Date.now();

  void (async () => {
    try {
      if (audio.state === 'suspended') await audio.resume();
      const buffer = await load(name);
      if (stopped || !buffer || audio.state !== 'running') return;
      // A ring that was asked for a while ago, held up by the autoplay rule or a
      // slow network, is still wanted if it loops; a one-off is out of date.
      if (!options.loop && Date.now() - asked > ONE_OFF_STALE_MS) return;
      const gain = audio.createGain();
      gain.gain.value = VOLUME[name];
      gain.connect(audio.destination);
      const node = audio.createBufferSource();
      node.buffer = buffer;
      node.loop = !!options.loop;
      node.connect(gain);
      node.onended = () => gain.disconnect();
      node.start();
      source = node;
      window.dispatchEvent(new CustomEvent(SOUND_EVENT, { detail: name }));
    } catch {
      // Not allowed yet, or no audio; the page says what happened anyway.
    }
  })();

  return stop;
}
