/**
 * The minimised call window's size and place: what it starts at, what it is
 * kept within, and what this device remembers of it.
 */

export const COMPACT_WIDTH = 320;
export const COMPACT_HEIGHT = 200;
// How small and how big the minimised window can be made.
export const COMPACT_MIN = { width: 240, height: 150 };
export const COMPACT_MAX = { width: 720, height: 540 };
// What a key press on the resize grip changes it by.
export const RESIZE_STEP = 24;
const SIZE_KEY = 'teamup.callWindow.size';

export type Size = { width: number; height: number };
export type Position = { x: number; y: number };

/** The size somebody left the window at on this device, or the usual one. */
export function savedSize(): Size {
  try {
    const raw = localStorage.getItem(SIZE_KEY);
    if (raw) {
      const { width, height } = JSON.parse(raw);
      if (Number.isFinite(width) && Number.isFinite(height)) return { width, height };
    }
  } catch {
    // Storage blocked or the value unreadable: the usual size.
  }
  return { width: COMPACT_WIDTH, height: COMPACT_HEIGHT };
}

/**
 * The window's size and place, kept within the screen: never smaller than its
 * minimum, never bigger than the room there is, and never pushed off an edge,
 * from where it could not be got back. Position is from the bottom right.
 */
export function fitCallWindow(size: Size, position: Position, screen: Size): { size: Size; position: Position } {
  const width = clamp(size.width, Math.min(COMPACT_MIN.width, screen.width - 16), Math.min(COMPACT_MAX.width, screen.width - 16));
  const height = clamp(size.height, Math.min(COMPACT_MIN.height, screen.height - 16), Math.min(COMPACT_MAX.height, screen.height - 16));
  return {
    size: { width, height },
    position: {
      x: clamp(position.x, 8, screen.width - width - 8),
      y: clamp(position.y, 8, screen.height - height - 8),
    },
  };
}

export function keepSize(size: Size) {
  try {
    localStorage.setItem(SIZE_KEY, JSON.stringify(size));
  } catch {
    // Not kept, which only means the usual size next time.
  }
}

export function viewport(): Size {
  return { width: window.innerWidth, height: window.innerHeight };
}

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), Math.max(min, max));
}
