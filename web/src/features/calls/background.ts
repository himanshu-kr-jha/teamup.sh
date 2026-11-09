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

const PROCESSOR_NAME = 'teamup-background';

/**
 * Keeps your camera's background as chosen: on whenever the camera is, through a
 * change of camera or a reconnect, switched in place when the choice changes, and
 * taken off entirely for none, so a plain camera costs nothing.
 */
export async function applyBackground(track: LocalVideoTrack, choice: BackgroundChoice): Promise<void> {
  const options = processorOptions(choice);
  const running = track.getProcessor();
  if (!options) {
    if (running) await track.stopProcessor();
    return;
  }
  const m = await import('@livekit/track-processors');
  if (running && running.name === PROCESSOR_NAME && running instanceof m.BackgroundProcessorWrapper) {
    await running.switchTo(options);
    return;
  }
  const processor = m.BackgroundProcessor(
    {
      ...options,
      assetPaths: {
        tasksVisionFileSet: __MEDIAPIPE_WASM__,
        modelAssetPath: '/mediapipe/selfie_segmenter.tflite',
      },
    },
    PROCESSOR_NAME
  );
  await track.setProcessor(processor);
}
