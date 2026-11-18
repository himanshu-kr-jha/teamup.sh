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

/** How wide the column of toasts is from a tablet up (toaster.tsx); below that it is the whole width. */
export const TOAST_COLUMN = 420;
/** The room a toast needs to be read: one short toast and the space around it. */
export const TOAST_ROOM = 120;

/**
 * Where toasts should start, in pixels from the bottom of the screen, so they
 * do not cover the minimised call window; null when they do not meet it.
 *
 * Toasts run up the right edge from just above the bottom navigation (none on
 * a wide screen). The window covers them only if it is over that column and
 * low enough to be in their way: dragged to the left, or up to the top, it is
 * not, and they stay where they always are. Over them, they go just above it,
 * but never so high that a toast would leave the screen.
 */
export function toastLift(position: Position, size: Size, screen: Size): number | null {
  const column = screen.width >= 768 ? Math.min(TOAST_COLUMN, screen.width) : screen.width;
  const base = screen.width >= 1024 ? 0 : 56;
  const overColumn = position.x < column;
  const inTheWay = position.y < base + TOAST_ROOM;
  if (!overColumn || !inTheWay) return null;
  return Math.min(position.y + size.height, Math.max(base, screen.height - TOAST_ROOM));
}
