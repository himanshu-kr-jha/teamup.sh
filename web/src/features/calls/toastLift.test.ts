import { describe, expect, it } from 'vitest';
import { TOAST_ROOM, toastLift } from './callWindow';

const laptop = { width: 1440, height: 900 };
const phone = { width: 390, height: 844 };
const window = { width: 320, height: 200 };

describe('toasts and the minimised call window', () => {
  it('go just above the window when it sits in their corner', () => {
    expect(toastLift({ x: 8, y: 24 }, window, laptop)).toBe(24 + 200);
    expect(toastLift({ x: 8, y: 72 }, window, phone)).toBe(72 + 200);
  });

  it('stay where they are when the window is out of their way', () => {
    // Dragged to the left of the column.
    expect(toastLift({ x: 1440 - 320 - 8, y: 24 }, window, laptop)).toBeNull();
    // Dragged to the top.
    expect(toastLift({ x: 8, y: 900 - 200 - 8 }, window, laptop)).toBeNull();
    expect(toastLift({ x: 8, y: 844 - 200 - 8 }, window, phone)).toBeNull();
  });

  it('never go off the top of the screen', () => {
    const tall = { width: 360, height: 820 };
    const lift = toastLift({ x: 8, y: 16 }, tall, phone)!;
    expect(lift).toBeLessThanOrEqual(phone.height - TOAST_ROOM);
    expect(lift).toBeGreaterThan(0);
  });

  it('on a phone the column is the whole width, so the window always meets it low down', () => {
    expect(toastLift({ x: 390 - 240 - 8, y: 72 }, { width: 240, height: 150 }, phone)).toBe(72 + 150);
  });
});
