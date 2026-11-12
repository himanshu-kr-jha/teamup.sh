import { describe, expect, it } from 'vitest';
import { COMPACT_MAX, COMPACT_MIN, fitCallWindow } from './callWindow';

const laptop = { width: 1440, height: 900 };
const phone = { width: 390, height: 844 };

describe('fitCallWindow', () => {
  it('keeps a size that fits as it is', () => {
    const fit = fitCallWindow({ width: 480, height: 300 }, { x: 24, y: 24 }, laptop);
    expect(fit).toEqual({ size: { width: 480, height: 300 }, position: { x: 24, y: 24 } });
  });

  it('is never made smaller than its minimum or bigger than its maximum', () => {
    expect(fitCallWindow({ width: 10, height: 10 }, { x: 24, y: 24 }, laptop).size).toEqual(COMPACT_MIN);
    expect(fitCallWindow({ width: 5000, height: 5000 }, { x: 24, y: 24 }, laptop).size).toEqual(COMPACT_MAX);
  });

  it('on a phone, is no wider than the screen less a margin', () => {
    expect(fitCallWindow({ width: 700, height: 300 }, { x: 8, y: 72 }, phone).size.width).toBe(phone.width - 16);
  });

  it('is never pushed off an edge, from where it could not be got back', () => {
    const fit = fitCallWindow({ width: 320, height: 200 }, { x: -500, y: 5000 }, laptop);
    expect(fit.position).toEqual({ x: 8, y: laptop.height - 200 - 8 });
  });

  it('comes back on screen when the phone is turned', () => {
    const before = fitCallWindow({ width: 360, height: 300 }, { x: 8, y: 500 }, phone);
    const turned = fitCallWindow(before.size, before.position, { width: 844, height: 390 });
    expect(turned.position.y + turned.size.height).toBeLessThanOrEqual(390 - 8);
    expect(turned.size.height).toBeGreaterThanOrEqual(COMPACT_MIN.height);
  });
});
