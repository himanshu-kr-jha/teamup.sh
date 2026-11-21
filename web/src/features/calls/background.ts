// What sits behind you on camera: nothing changed, your room blurred, or one of
// the organization's backgrounds. The choice is yours and this browser's: it is
// kept here and applied to your camera in every call, without anybody else's say.
// The pictures to choose from are the organization's alone (the owner's request
// of 19 November 2026): an administrator puts them in, on Organization >
// Appearance, and there are no others.
//
// The work happens on this device. MediaPipe finds the person in each frame and
// the rest is blurred or replaced before the video leaves; the runtime and its
// model are served by TeamUp itself (vite.config.ts, public/mediapipe/).

import { useEffect, useState, useSyncExternalStore } from 'react';
import type { LocalVideoTrack, ProcessorOptions, Room, RoomOptions, Track, TrackProcessor } from 'livekit-client';
import type { SwitchBackgroundProcessorOptions } from '@livekit/track-processors';
import type { CallBackgrounds } from '@/shared/api/client';

export type BackgroundChoice =
  | { kind: 'none' }
  | { kind: 'blur'; strength: 'light' | 'strong' }
  /** One of the organization's own pictures, by its id. */
  | { kind: 'picture'; id: string }
  /** The organization's template with your own name on it (companyBackground.ts). */
  | { kind: 'company'; theme: 'dark' | 'light' };

/** How hard each blur is, in the processor's own units. */
const BLUR_RADIUS = { light: 6, strong: 16 } as const;

const CHOICE_KEY = 'teamup.call.background';
/**
 * Where a picture of your own used to be kept, before only the organization's
 * could be chosen. Cleared whenever the choice is read, so it does not linger.
 */
const OLD_CUSTOM_KEY = 'teamup.call.background.custom';

const NONE: BackgroundChoice = { kind: 'none' };

function read(): BackgroundChoice {
  try {
    localStorage.removeItem(OLD_CUSTOM_KEY);
  } catch {
    // Nothing kept, or nothing to keep it in.
  }
  try {
    const raw = localStorage.getItem(CHOICE_KEY);
    if (!raw) return NONE;
    const parsed = JSON.parse(raw) as BackgroundChoice;
    if (parsed.kind === 'blur' && (parsed.strength === 'light' || parsed.strength === 'strong')) return parsed;
    // Kept even before the organization's pictures and templates are known; it
    // is not applied until they are, and is forgotten if it is not among them
    // (stillAllowed).
    if (parsed.kind === 'picture' && typeof parsed.id === 'string' && parsed.id) return { kind: 'picture', id: parsed.id };
    if (parsed.kind === 'company' && (parsed.theme === 'dark' || parsed.theme === 'light')) return parsed;
    // Anything else, such as a picture that came with TeamUp or one of your
    // own from before, is no longer on offer.
    forget();
    return NONE;
  } catch {
    forget();
    return NONE;
  }
}

function forget() {
  try {
    localStorage.removeItem(CHOICE_KEY);
  } catch {
    // Not kept anyway.
  }
}

// One choice for the whole page, so the picker and the camera agree at once.
let current: BackgroundChoice | null = null;
const listeners = new Set<() => void>();
const choiceNow = () => (current ??= read());

/** The choice now, outside React. */
export const currentBackgroundChoice = choiceNow;

export function setBackgroundChoice(choice: BackgroundChoice) {
  current = choice;
  try {
    if (choice.kind === 'none') localStorage.removeItem(CHOICE_KEY);
    else localStorage.setItem(CHOICE_KEY, JSON.stringify(choice));
  } catch {
    // Private browsing: it still applies to this call, it is just not remembered.
  }
  syncKeep();
  listeners.forEach((l) => l());
}

export function useBackgroundChoice(): BackgroundChoice {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    choiceNow,
    () => NONE
  );
}

/** Whether two choices are the same one, to mark which is selected. */
export function sameChoice(a: BackgroundChoice, b: BackgroundChoice): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/**
 * Whether a choice is still on offer, given what the organization has now: a
 * picture or template it has since taken away is not. Blurs and none always are.
 */
export function stillAllowed(choice: BackgroundChoice, backgrounds: CallBackgrounds): boolean {
  switch (choice.kind) {
    case 'picture':
      return (backgrounds.pictures ?? []).some((p) => p.id === choice.id);
    case 'company':
      return !!backgrounds[choice.theme];
    default:
      return true;
  }
}

/**
 * What the processor is told for a choice; null for none, which takes the
 * processor off. For one of the organization's pictures, `picture` is its
 * address; for the company background, the picture already made with your name
 * on it. Either is null while there is none.
 */
export function processorOptions(choice: BackgroundChoice, picture?: string | null): SwitchBackgroundProcessorOptions | null {
  switch (choice.kind) {
    case 'blur':
      return { mode: 'background-blur', blurRadius: BLUR_RADIUS[choice.strength] };
    case 'picture':
    case 'company':
      return picture ? { mode: 'virtual-background', imagePath: picture } : null;
    default:
      return null;
  }
}

/**
 * Whether this browser can change a camera's background. Loaded on demand, so
 * the processor and MediaPipe are only fetched by somebody who uses them.
 */
export function useBackgroundsSupported(): boolean | null {
  const [supported, setSupported] = useState<boolean | null>(null);
  useEffect(() => {
    let live = true;
    import('@livekit/track-processors')
      .then((m) => live && setSupported(m.supportsBackgroundProcessors()))
      .catch(() => live && setSupported(false));
    return () => {
      live = false;
    };
  }, []);
  return supported;
}

// ── Colour filters ────────────────────────────────────────────────────────

/**
 * The looks on offer: a canvas filter, as CSS writes it, and for the two that
 * shift the colour a wash laid over the picture in soft light, which tints it the
 * way a warm or a cool light would without turning skin a strange colour as
 * rotating the hue does. Natural leaves the picture as it is.
 */
export const LOOKS = [
  { id: 'natural', label: 'Natural', filter: '', wash: '' },
  { id: 'warm', label: 'Warm', filter: 'saturate(1.1) brightness(1.03)', wash: 'rgba(255, 150, 60, 0.32)' },
  { id: 'cool', label: 'Cool', filter: 'saturate(0.95) brightness(1.04)', wash: 'rgba(70, 140, 255, 0.32)' },
  { id: 'mono', label: 'Black & white', filter: 'grayscale(1) contrast(1.08)', wash: '' },
  { id: 'vintage', label: 'Vintage', filter: 'sepia(0.55) contrast(0.95) brightness(1.04) saturate(0.85)', wash: '' },
  { id: 'bright', label: 'Bright', filter: 'brightness(1.15) contrast(1.04) saturate(1.05)', wash: '' },
  { id: 'vivid', label: 'Vivid', filter: 'saturate(1.45) contrast(1.06)', wash: '' },
] as const;

export type LookId = (typeof LOOKS)[number]['id'];

const LOOK_KEY = 'teamup.call.look';

function readLook(): LookId {
  try {
    const raw = localStorage.getItem(LOOK_KEY);
    return LOOKS.some((l) => l.id === raw) ? (raw as LookId) : 'natural';
  } catch {
    return 'natural';
  }
}

let currentLook: LookId | null = null;
const lookNow = () => (currentLook ??= readLook());
/** The look now, outside React. */
export const currentLookChoice = lookNow;

export function setLook(look: LookId) {
  currentLook = look;
  try {
    localStorage.setItem(LOOK_KEY, look);
  } catch {
    // Not remembered, but still applied to this call.
  }
  syncKeep();
  listeners.forEach((l) => l());
}

export function useLook(): LookId {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    lookNow,
    () => 'natural' as LookId
  );
}

export const lookFilter = (id: LookId) => LOOKS.find((l) => l.id === id)?.filter ?? '';
export const lookWash = (id: LookId) => LOOKS.find((l) => l.id === id)?.wash ?? '';

/** Whether this browser can draw a frame through a filter; Safari before 18 cannot. */
export function supportsLooks(): boolean {
  try {
    const ctx = new OffscreenCanvas(1, 1).getContext('2d');
    return !!ctx && 'filter' in ctx;
  } catch {
    return false;
  }
}

// ── Putting them on the camera ────────────────────────────────────────────

/** Whether a background or a look is chosen: anything that has to go on the camera. */
export function wantsEffects(): boolean {
  return currentBackgroundChoice().kind !== 'none' || currentLookChoice() !== 'natural';
}

type EffectsModule = typeof import('./cameraProcessor');
let effectsModule: Promise<EffectsModule> | null = null;

/**
 * The processor's code, loaded once, told whether to keep a processor made ready
 * from one camera to the next: while anything is chosen, so the next call's
 * camera does not set MediaPipe up again.
 */
function loadEffects(): Promise<EffectsModule> {
  effectsModule ??= import('./cameraProcessor').catch((err) => {
    effectsModule = null;
    throw err;
  });
  return effectsModule.then((m) => {
    m.keepEffects(wantsEffects());
    return m;
  });
}

function syncKeep() {
  effectsModule?.then((m) => m.keepEffects(wantsEffects())).catch(() => undefined);
}

// Cameras waiting for their background before they are sent, for "Starting your background".
let starting = 0;
const startingListeners = new Set<() => void>();
const setStarting = (delta: number) => {
  starting = Math.max(0, starting + delta);
  startingListeners.forEach((l) => l());
};

/** Whether a camera is being held back until its background is ready. */
export function useStartingBackground(): boolean {
  return useSyncExternalStore(
    (l) => {
      startingListeners.add(l);
      return () => startingListeners.delete(l);
    },
    () => starting > 0,
    () => false
  );
}

/**
 * What a camera starts with, before the organization's pictures are known: the
 * chosen blur, or for a picture the strong blur, which hides the room as well,
 * until the picture itself is put on in place (useCameraEffects).
 */
function startingSetting(): { background: import('./cameraProcessor').BackgroundSetting; look: { filter: string; wash: string } } {
  const choice = currentBackgroundChoice();
  const look = currentLookChoice();
  return {
    background: choice.kind === 'none' ? null : { blurRadius: BLUR_RADIUS[choice.kind === 'blur' ? choice.strength : 'strong'] },
    look: { filter: lookFilter(look), wash: lookWash(look) },
  };
}

/**
 * The processor every camera is opened with, so that one with a background
 * chosen is sent with it from the first frame and never as the room behind you
 * is: LiveKit puts it on before publishing, and publishes once it is ready.
 * Without anything chosen when the camera opens it does nothing, and loads
 * nothing; the camera is sent as it is.
 *
 * If the processor cannot be made on this device, nothing is sent rather than
 * the room: useCameraEffects then says so and goes back to no background, as it
 * does for any effect that fails.
 */
export class NewCameraProcessor implements TrackProcessor<Track.Kind.Video> {
  readonly name = 'teamup-camera';
  /** Published in a call (onPublish), so a camera restarted later is not held. */
  private published = false;

  /**
   * `inCall`: the camera of a call, whose frames are worked on only once it is
   * published (processorForNewCamera). The preview before a call is never
   * published, so it is not held.
   */
  constructor(private readonly inCall = false) {}

  async onPublish(room: Room) {
    this.published = true;
    await this.inner?.onPublish?.(room);
  }
  /** The processor doing the work, once there is one. */
  inner?: TrackProcessor<Track.Kind.Video>;
  /** A track that never shows a frame, sent in place of the camera when the processor failed. */
  private blank?: MediaStreamTrack;

  get processedTrack(): MediaStreamTrack | undefined {
    return this.inner?.processedTrack ?? this.blank;
  }

  /** Whether this does nothing, and the camera goes as it is. */
  get idle(): boolean {
    return !this.inner && !this.blank;
  }

  async init(opts: ProcessorOptions<Track.Kind.Video>) {
    if (!wantsEffects()) return;
    const { background, look } = startingSetting();
    const settings = opts.track.getSettings();
    setStarting(1);
    try {
      const m = await loadEffects();
      const inner = m.processorForNewCamera(
        background,
        look,
        { width: settings.width, height: settings.height },
        this.inCall && !this.published
      );
      if (!inner) return;
      await inner.init(opts);
      this.inner = inner;
    } catch {
      this.inner = undefined;
      this.blank = document.createElement('canvas').captureStream(0).getVideoTracks()[0];
    } finally {
      setStarting(-1);
    }
  }

  async restart(opts: ProcessorOptions<Track.Kind.Video>) {
    if (this.inner) return this.inner.restart(opts);
    await this.destroy();
    await this.init(opts);
  }

  async destroy() {
    const inner = this.inner;
    this.inner = undefined;
    this.blank?.stop();
    this.blank = undefined;
    await inner?.destroy();
  }
}

/**
 * A call's room options with NewCameraProcessor on every camera it opens. A new
 * one for each room, which LiveKit builds once.
 */
export function withCameraEffects(options: RoomOptions): RoomOptions {
  return { ...options, videoCaptureDefaults: { ...options.videoCaptureDefaults, processor: new NewCameraProcessor(true) } };
}

/**
 * Whether a camera has an effect on it, or is held back for one: not when all
 * it has is an idle NewCameraProcessor, which every camera in a call opens with.
 */
export function hasCameraEffects(track: LocalVideoTrack): boolean {
  const held = track.getProcessor();
  return held instanceof NewCameraProcessor ? !held.idle : !!held;
}

/** The processor that does the work on a track, under NewCameraProcessor if that is what is on it. */
function workingProcessor(track: LocalVideoTrack) {
  const held = track.getProcessor();
  return held instanceof NewCameraProcessor ? held.inner : held;
}

/**
 * Starts setting up the processor now, for somebody with a background chosen,
 * so the camera is quick to start: on the screen before a call.
 */
export function warmCameraEffects() {
  if (!wantsEffects()) return;
  void loadEffects()
    .then((m) => m.warmEffects())
    .catch(() => undefined);
}

/**
 * Keeps your camera as chosen, background and look together: switched in place
 * when either changes, so there is no flicker and no need to turn the camera off
 * and on; and taken off entirely when both are plain, so a plain camera costs
 * nothing. Call it only while the camera is live: a processor set on a camera
 * that is off waits for a picture that is not coming.
 *
 * Nobody sees the video stand still meanwhile: a new processor is made ready
 * while the camera goes on sending as it is, and put on once it is; one taken
 * off hands the camera back to the call before it goes.
 */
export async function applyCameraEffects(
  track: LocalVideoTrack,
  choice: BackgroundChoice,
  look: LookId,
  picture?: string | null
): Promise<void> {
  const options = processorOptions(choice, picture);
  const filter = { filter: lookFilter(look), wash: lookWash(look) };
  const plainLook = !filter.filter && !filter.wash;
  const held = track.getProcessor();
  const running = workingProcessor(track);
  if (!options && plainLook) {
    if (running) await takeEffectsOff(track);
    // One that failed sends nothing; with nothing chosen, the camera goes as it is.
    else if (held?.processedTrack) await plainCamera(track);
    return;
  }
  const m = await loadEffects();
  const background: import('./cameraProcessor').BackgroundSetting = !options
    ? null
    : options.mode === 'background-blur'
      ? { blurRadius: options.blurRadius ?? 10 }
      : options.mode === 'virtual-background'
        ? { imagePath: options.imagePath }
        : null;
  if (running && m.isCameraProcessor(running)) {
    await m.updateCameraProcessor(running, background, filter);
    return;
  }
  // An idle one is simply replaced, once the new one is ready (setProcessor).
  if (held && !(held instanceof NewCameraProcessor && held.idle)) await track.stopProcessor();
  const settings = track.getSourceTrackSettings();
  await m.attachCameraProcessor(
    (processor) => track.setProcessor(processor),
    background,
    filter,
    { width: settings.width, height: settings.height },
    () => !track.isMuted && track.mediaStreamTrack?.readyState === 'live'
  );
}

/**
 * Takes the processor off a camera. Its picture goes plain at once, the call is
 * given the camera as it is, and only then does the processor go, so the video
 * others see never waits for it.
 */
async function takeEffectsOff(track: LocalVideoTrack) {
  const running = workingProcessor(track);
  if (!running) return;
  const m = await loadEffects();
  if (m.isCameraProcessor(running)) {
    await m.updateCameraProcessor(running, null, { filter: '', wash: '' });
    const camera = m.cameraUnder(running);
    if (camera?.readyState === 'live') await track.sender?.replaceTrack(camera).catch(() => undefined);
  }
  await plainCamera(track);
}

/**
 * Leaves the camera as it is, with an idle NewCameraProcessor on it rather than
 * none: LiveKit restarts it when the camera comes back on, so a background chosen
 * while the camera was off is on it before its first frame goes out.
 */
async function plainCamera(track: LocalVideoTrack) {
  await track.setProcessor(new NewCameraProcessor());
}

/**
 * Fetches what a background needs ahead of time, so the first one is quick: when
 * the picker opens, and once the page is idle in a call with the camera on. Not
 * on a connection that asks to save data, unless the picker is opened.
 */
export function preloadCameraEffects(whenIdle = false) {
  const saveData = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData;
  if (whenIdle && saveData) return () => undefined;
  const load = () => void loadEffects().then((m) => m.preloadEffects()).catch(() => undefined);
  if (!whenIdle) {
    load();
    return () => undefined;
  }
  if (typeof requestIdleCallback === 'function') {
    const id = requestIdleCallback(load, { timeout: 10_000 });
    return () => cancelIdleCallback(id);
  }
  const timer = setTimeout(load, 3_000);
  return () => clearTimeout(timer);
}
