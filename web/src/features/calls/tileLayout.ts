/**
 * How a call's tiles are arranged. Pure, so the arithmetic is tested rather than
 * looked at: everything here takes numbers and ids and returns numbers and ids.
 */

export const TILE_GAP = 8;
/** Above this many people the grid pages rather than shrinking faces to nothing. */
export const MAX_TILES_PER_PAGE = 16;
/** The corner window has room for this many and no room for paging controls. */
export const MAX_TILES_COMPACT = 4;

const ASPECT = 16 / 9;

export interface Grid {
  columns: number;
  rows: number;
  tileWidth: number;
  tileHeight: number;
}

function gridOf(columns: number, count: number, width: number, height: number, gap: number): Grid {
  const rows = Math.ceil(count / columns);
  const cellWidth = (width - gap * (columns - 1)) / columns;
  const cellHeight = (height - gap * (rows - 1)) / rows;
  if (cellWidth <= 0 || cellHeight <= 0) return { columns, rows, tileWidth: 0, tileHeight: 0 };
  const tileWidth = Math.floor(Math.min(cellWidth, cellHeight * ASPECT));
  return { columns, rows, tileWidth, tileHeight: Math.floor(tileWidth / ASPECT) };
}

/**
 * The column count that makes tiles largest at 16:9 in the space available, with one
 * exception. Two people in a space wider than it is tall sit side by side, even
 * where stacking would make them a little larger: a conversation reads across, and
 * a laptop window stacking two faces looks like a mistake (decision 37). Two people
 * on a phone held upright still stack, simply because the space is taller than wide.
 */
export function bestGrid(count: number, width: number, height: number, gap: number = TILE_GAP): Grid {
  if (count <= 0) return { columns: 0, rows: 0, tileWidth: 0, tileHeight: 0 };
  if (count === 2 && width > height) {
    const sideBySide = gridOf(2, count, width, height, gap);
    if (sideBySide.tileWidth > 0) return sideBySide;
  }
  let best: Grid = { columns: 1, rows: count, tileWidth: 0, tileHeight: 0 };
  for (let columns = 1; columns <= count; columns++) {
    const grid = gridOf(columns, count, width, height, gap);
    if (grid.tileWidth > best.tileWidth) best = grid;
  }
  return best;
}

export function pageCount(total: number, pageSize: number): number {
  return Math.max(1, Math.ceil(total / pageSize));
}

/** A page that still exists after somebody left. */
export function clampPage(page: number, total: number, pageSize: number): number {
  return Math.min(Math.max(page, 0), pageCount(total, pageSize) - 1);
}

export function pageOf<T>(order: T[], page: number, pageSize: number): T[] {
  return order.slice(page * pageSize, (page + 1) * pageSize);
}

/**
 * Whoever is speaking stays on the current page. Somebody speaking elsewhere swaps
 * places with the last person here who is not speaking. It is a swap and it stays,
 * so tiles do not jump back the moment somebody draws breath, and it is the same
 * array when there is nothing to do, so it is safe to run on every change.
 */
export function promoteSpeakers(order: string[], page: number, pageSize: number, speakers: string[]): string[] {
  const start = page * pageSize;
  const end = Math.min(start + pageSize, order.length);
  let next = order;
  for (const speaker of speakers) {
    const at = next.indexOf(speaker);
    if (at === -1 || (at >= start && at < end)) continue;
    let swap = -1;
    for (let i = end - 1; i >= start; i--) {
      if (!speakers.includes(next[i])) {
        swap = i;
        break;
      }
    }
    if (swap === -1) break;
    if (next === order) next = [...order];
    next[at] = next[swap];
    next[swap] = speaker;
  }
  return next;
}

/** Keeps everybody where they were, drops who left and adds who came at the end. */
export function reconcileOrder(previous: string[], current: string[]): string[] {
  const present = new Set(current);
  const kept = previous.filter((id) => present.has(id));
  const known = new Set(kept);
  const added = current.filter((id) => !known.has(id));
  if (added.length === 0 && kept.length === previous.length) return previous;
  return [...kept, ...added];
}

/**
 * Which share is on stage. What somebody clicked, while it lasts, and otherwise the
 * first, so a second person starting to share does not pull the stage away from
 * what everybody is looking at.
 */
export function stageShare(shares: string[], chosen: string | null): string | null {
  if (shares.length === 0) return null;
  return chosen && shares.includes(chosen) ? chosen : shares[0];
}
