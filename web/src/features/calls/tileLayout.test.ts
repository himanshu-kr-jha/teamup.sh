import { describe, expect, it } from 'vitest';
import {
  MAX_TILES_PER_PAGE,
  bestGrid,
  clampPage,
  pageCount,
  pageOf,
  promoteSpeakers,
  reconcileOrder,
  stageShare,
} from './tileLayout';

describe('bestGrid', () => {
  it('fills the space with one tile at 16:9', () => {
    const grid = bestGrid(1, 1600, 900, 8);
    expect(grid).toMatchObject({ columns: 1, rows: 1, tileWidth: 1600, tileHeight: 900 });
  });

  it('is limited by height when the space is wider than 16:9', () => {
    const grid = bestGrid(1, 2000, 450, 8);
    expect(grid.tileHeight).toBe(450);
    expect(grid.tileWidth).toBe(800);
  });

  it('puts two people side by side on a computer', () => {
    expect(bestGrid(2, 1400, 650, 8).columns).toBe(2);
  });

  it('puts two people side by side on a laptop window, where stacking would be slightly larger', () => {
    const grid = bestGrid(2, 1016, 680, 8);
    expect(grid).toMatchObject({ columns: 2, rows: 1, tileWidth: 504, tileHeight: 283 });
    // The sum this overrides: stacked they would be 597 wide.
    expect(Math.floor(((680 - 8) / 2) * (16 / 9))).toBe(597);
  });

  it('keeps two people side by side in any window wider than tall, and stacks them only upright', () => {
    expect(bestGrid(2, 769, 768, 8).columns).toBe(2);
    expect(bestGrid(2, 768, 768, 8).columns).toBe(1);
    expect(bestGrid(2, 320, 165, 8).columns).toBe(2);
  });

  it('survives two people in a window too narrow for two columns', () => {
    expect(bestGrid(2, 6, 4, 8).tileWidth).toBe(0);
  });

  it('stacks two people on a phone held upright', () => {
    const grid = bestGrid(2, 359, 560, 8);
    expect(grid.columns).toBe(1);
    expect(grid.rows).toBe(2);
    expect(grid.tileWidth).toBe(359);
  });

  it('picks the column count that makes tiles largest, whatever the shape, two people aside', () => {
    // Brute force over every possibility: nothing may beat the chosen one.
    for (const [w, h] of [[1280, 720], [375, 600], [800, 300], [320, 165], [1920, 1000]]) {
      for (let count = 1; count <= 16; count++) {
        if (count === 2 && w > h) continue;
        const best = bestGrid(count, w, h, 8);
        for (let columns = 1; columns <= count; columns++) {
          const rows = Math.ceil(count / columns);
          const width = (w - 8 * (columns - 1)) / columns;
          const height = (h - 8 * (rows - 1)) / rows;
          const tileWidth = Math.min(width, (height * 16) / 9);
          expect(best.tileWidth).toBeGreaterThanOrEqual(Math.floor(tileWidth) - 1);
        }
      }
    }
  });

  it('never overflows the space it was given', () => {
    for (let count = 1; count <= 16; count++) {
      const { columns, rows, tileWidth, tileHeight } = bestGrid(count, 987, 613, 8);
      expect(columns * tileWidth + (columns - 1) * 8).toBeLessThanOrEqual(987);
      expect(rows * tileHeight + (rows - 1) * 8).toBeLessThanOrEqual(613);
    }
  });

  it('survives having no space or nobody yet', () => {
    expect(bestGrid(0, 800, 600, 8)).toMatchObject({ columns: 0, rows: 0, tileWidth: 0, tileHeight: 0 });
    expect(bestGrid(4, 0, 0, 8)).toMatchObject({ tileWidth: 0, tileHeight: 0 });
    expect(bestGrid(4, 4, 4, 8).tileWidth).toBe(0);
  });
});

describe('paging', () => {
  const people = Array.from({ length: 40 }, (_, i) => `p${i}`);

  it('does not page up to 16', () => {
    expect(pageCount(MAX_TILES_PER_PAGE, MAX_TILES_PER_PAGE)).toBe(1);
    expect(pageCount(17, MAX_TILES_PER_PAGE)).toBe(2);
    expect(pageCount(0, MAX_TILES_PER_PAGE)).toBe(1);
  });

  it('slices a page', () => {
    expect(pageOf(people, 1, 16)).toEqual(people.slice(16, 32));
    expect(pageOf(people, 2, 16)).toEqual(people.slice(32));
  });

  it('comes back to a page that exists when people leave', () => {
    expect(clampPage(3, 17, 16)).toBe(1);
    expect(clampPage(-1, 17, 16)).toBe(0);
    expect(clampPage(1, 16, 16)).toBe(0);
  });
});

describe('promoteSpeakers', () => {
  const order = ['a', 'b', 'c', 'd', 'e', 'f'];

  it('leaves the order alone when the speaker is already on the page', () => {
    expect(promoteSpeakers(order, 0, 4, ['b'])).toBe(order);
  });

  it('brings somebody speaking on another page onto this one, by swapping', () => {
    const next = promoteSpeakers(order, 0, 4, ['f']);
    expect(pageOf(next, 0, 4)).toContain('f');
    expect([...next].sort()).toEqual([...order].sort());
    // The last tile makes room; everybody else stays put.
    expect(next.slice(0, 3)).toEqual(['a', 'b', 'c']);
    expect(next[3]).toBe('f');
    expect(next[5]).toBe('d');
  });

  it('does not push out somebody who is also speaking', () => {
    const next = promoteSpeakers(order, 0, 4, ['d', 'f']);
    expect(pageOf(next, 0, 4)).toEqual(expect.arrayContaining(['d', 'f']));
    expect(next[2]).toBe('f');
  });

  it('works from a later page', () => {
    const next = promoteSpeakers(order, 1, 4, ['a']);
    expect(pageOf(next, 1, 4)).toContain('a');
    expect(next).toHaveLength(6);
  });

  it('ignores somebody it does not know', () => {
    expect(promoteSpeakers(order, 0, 4, ['zz'])).toBe(order);
  });

  it('stays stable when applied again', () => {
    const once = promoteSpeakers(order, 0, 4, ['f']);
    expect(promoteSpeakers(once, 0, 4, ['f'])).toBe(once);
  });
});

describe('reconcileOrder', () => {
  it('keeps who was where, drops who left and adds who came at the end', () => {
    expect(reconcileOrder(['c', 'a', 'b'], ['a', 'c', 'd'])).toEqual(['c', 'a', 'd']);
  });

  it('starts from the given order', () => {
    expect(reconcileOrder([], ['a', 'b'])).toEqual(['a', 'b']);
  });

  it('hands back the same array when nothing changed', () => {
    const prior = ['b', 'a'];
    expect(reconcileOrder(prior, ['a', 'b'])).toBe(prior);
  });
});

describe('stageShare', () => {
  it('is nobody with no shares', () => {
    expect(stageShare([], null)).toBeNull();
    expect(stageShare([], 'a')).toBeNull();
  });

  it('is the first share by default, so a second person sharing does not snatch the stage', () => {
    expect(stageShare(['a', 'b'], null)).toBe('a');
  });

  it('follows what somebody clicked', () => {
    expect(stageShare(['a', 'b'], 'b')).toBe('b');
  });

  it('falls back when the chosen share stops', () => {
    expect(stageShare(['a'], 'b')).toBe('a');
  });
});
