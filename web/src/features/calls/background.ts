// What sits behind you on camera: nothing changed, your room blurred, or a
// picture. The choice is yours and this browser's: it is kept here and applied
// to your camera in every call, without anybody else's say.
//
// The work happens on this device. MediaPipe finds the person in each frame and
// the rest is blurred or replaced before the video leaves; the runtime and its
// model are served by TeamUp itself (vite.config.ts, public/mediapipe/).

import { useEffect, useState, useSyncExternalStore } from 'react';
import type { LocalVideoTrack } from 'livekit-client';
import type { SwitchBackgroundProcessorOptions } from '@livekit/track-processors';

export type BackgroundChoice =
  | { kind: 'none' }
  | { kind: 'blur'; strength: 'light' | 'strong' }
  | { kind: 'image'; id: string }
  | { kind: 'custom' };

/** The pictures that come with TeamUp, drawn by web/scripts/make-backgrounds.py. */
export const PICTURES = [
  { id: 'studio', label: 'Studio' },
  { id: 'indigo', label: 'Indigo' },
  { id: 'dawn', label: 'Dawn' },
  { id: 'ocean', label: 'Ocean' },
  { id: 'forest', label: 'Forest' },
  { id: 'night', label: 'Night' },
] as const;

export const pictureURL = (id: string) => `/backgrounds/${id}.jpg`;
export const thumbURL = (id: string) => `/backgrounds/${id}-thumb.jpg`;

/** How hard each blur is, in the processor's own units. */
const BLUR_RADIUS = { light: 6, strong: 16 } as const;

const CHOICE_KEY = 'teamup.call.background';
const CUSTOM_KEY = 'teamup.call.background.custom';
/** The size an uploaded picture is brought to: the camera's own, so nothing is wasted. */
const CUSTOM_WIDTH = 1280;
const CUSTOM_HEIGHT = 720;

const NONE: BackgroundChoice = { kind: 'none' };

function read(): BackgroundChoice {
  try {
    const raw = localStorage.getItem(CHOICE_KEY);
    if (!raw) return NONE;
    const parsed = JSON.parse(raw) as BackgroundChoice;
    if (parsed.kind === 'blur' && (parsed.strength === 'light' || parsed.strength === 'strong')) return parsed;
    if (parsed.kind === 'image' && PICTURES.some((p) => p.id === parsed.id)) return parsed;
    if (parsed.kind === 'custom' && readCustom()) return parsed;
    return NONE;
  } catch {
    return NONE;
  }
}

function readCustom(): string | null {
  try {
    return localStorage.getItem(CUSTOM_KEY);
  } catch {
    return null;
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
    localStorage.setItem(CHOICE_KEY, JSON.stringify(choice));
  } catch {
    // Private browsing: it still applies to this call, it is just not remembered.
  }
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

/** Your own picture, if you have added one. */
export function customPicture(): string | null {
  return readCustom();
}

/**
 * Takes a picture you chose, crops it to the camera's shape from its middle,
 * and keeps it in this browser. Rejects anything that is not a picture.
 */
export async function saveCustomPicture(file: File): Promise<void> {
  if (!file.type.startsWith('image/')) throw new Error('Choose a picture: a JPEG, PNG or WebP file.');
  const url = URL.createObjectURL(file);
  try {
    const img = await new Promise<HTMLImageElement>((resolve, reject) => {
      const i = new Image();
      i.onload = () => resolve(i);
      i.onerror = () => reject(new Error('That picture could not be opened.'));
      i.src = url;
    });
    const canvas = document.createElement('canvas');
    canvas.width = CUSTOM_WIDTH;
    canvas.height = CUSTOM_HEIGHT;
    const scale = Math.max(CUSTOM_WIDTH / img.naturalWidth, CUSTOM_HEIGHT / img.naturalHeight);
    const w = img.naturalWidth * scale;
    const h = img.naturalHeight * scale;
    canvas.getContext('2d')!.drawImage(img, (CUSTOM_WIDTH - w) / 2, (CUSTOM_HEIGHT - h) / 2, w, h);
    const data = canvas.toDataURL('image/jpeg', 0.85);
    try {
      localStorage.setItem(CUSTOM_KEY, data);
    } catch {
      throw new Error('This browser has no room to keep that picture.');
    }
  } finally {
    URL.revokeObjectURL(url);
  }
}

/** What the processor is told for a choice; null for none, which takes the processor off. */
export function processorOptions(choice: BackgroundChoice): SwitchBackgroundProcessorOptions | null {
  switch (choice.kind) {
    case 'blur':
      return { mode: 'background-blur', blurRadius: BLUR_RADIUS[choice.strength] };
    case 'image':
      return { mode: 'virtual-background', imagePath: pictureURL(choice.id) };
    case 'custom': {
      const data = readCustom();
      return data ? { mode: 'virtual-background', imagePath: data } : null;
    }
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

/**
 * Keeps your camera as chosen, background and look together: switched in place
 * when either changes, so there is no flicker and no need to turn the camera off
 * and on; and taken off entirely when both are plain, so a plain camera costs
 * nothing. Call it only while the camera is live: a processor set on a camera
 * that is off waits for a picture that is not coming.
 */
export async function applyCameraEffects(track: LocalVideoTrack, choice: BackgroundChoice, look: LookId): Promise<void> {
  const options = processorOptions(choice);
  const filter = { filter: lookFilter(look), wash: lookWash(look) };
  const plainLook = !filter.filter && !filter.wash;
  const running = track.getProcessor();
  if (!options && plainLook) {
    if (running) await track.stopProcessor();
    return;
  }
  const m = await import('./cameraProcessor');
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
  if (running) await track.stopProcessor();
  const processor = m.createCameraProcessor(background, filter, {
    tasksVisionFileSet: __MEDIAPIPE_WASM__,
    modelAssetPath: '/mediapipe/selfie_segmenter.tflite',
  });
  const visible = m.shown(processor);
  await track.setProcessor(processor);
  await visible;
}
