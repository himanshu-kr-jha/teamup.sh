import { describe, it, expect } from 'vitest';
import { HISTORY_PAGE_SIZE, olderCursor, mergeOlder, reachedStart } from './history';

const at = (id: string, createdAt: string, extra: Record<string, unknown> = {}) => ({
  id,
  createdAt,
  ...extra,
});

describe('olderCursor', () => {
  it('is the timestamp of the oldest message on screen', () => {
    const messages = [
      at('a', '2026-08-01T10:00:00.000Z'),
      at('b', '2026-08-01T11:00:00.000Z'),
      at('c', '2026-08-01T12:00:00.000Z'),
    ];
    expect(olderCursor(messages)).toBe('2026-08-01T10:00:00.000Z');
  });

  it('has nothing to page back from on an empty channel', () => {
    expect(olderCursor([])).toBeUndefined();
  });

  // An optimistic message carries a client clock and no server row, so asking
  // the server for everything before it would be asking about a time that
  // never happened.
  it('ignores messages that have not been accepted yet', () => {
    const messages = [
      at('temp-1', '2026-08-01T09:00:00.000Z', { pending: true }),
      at('b', '2026-08-01T11:00:00.000Z'),
    ];
    expect(olderCursor(messages)).toBe('2026-08-01T11:00:00.000Z');
  });

  it('gives up rather than guessing when everything is still pending', () => {
    expect(olderCursor([at('temp-1', '2026-08-01T09:00:00.000Z', { pending: true })])).toBeUndefined();
  });
});

describe('mergeOlder', () => {
  it('puts a fetched page in front of what is already on screen', () => {
    const older = [at('a', '2026-08-01T09:00:00.000Z')];
    const recent = [at('b', '2026-08-01T10:00:00.000Z')];
    expect(mergeOlder(older, recent).map((m) => m.id)).toEqual(['a', 'b']);
  });

  // `before` is exclusive so pages do not normally overlap, but a message can
  // arrive over the socket while a page is in flight and land in both.
  it('shows a message that appears in both halves once', () => {
    const older = [at('a', '2026-08-01T09:00:00.000Z'), at('b', '2026-08-01T10:00:00.000Z')];
    const recent = [at('b', '2026-08-01T10:00:00.000Z'), at('c', '2026-08-01T11:00:00.000Z')];
    expect(mergeOlder(older, recent).map((m) => m.id)).toEqual(['a', 'b', 'c']);
  });

  it('keeps the fresher copy of a duplicate', () => {
    const older = [at('b', '2026-08-01T10:00:00.000Z', { content: 'stale' })];
    const recent = [at('b', '2026-08-01T10:00:00.000Z', { content: 'edited' })];
    expect(mergeOlder(older, recent)[0]).toMatchObject({ content: 'edited' });
  });

  it('reads in time order however the two halves interleave', () => {
    const older = [at('a', '2026-08-01T09:00:00.000Z'), at('c', '2026-08-01T11:00:00.000Z')];
    const recent = [at('b', '2026-08-01T10:00:00.000Z')];
    expect(mergeOlder(older, recent).map((m) => m.id)).toEqual(['a', 'b', 'c']);
  });

  it('leaves a channel with no history alone', () => {
    expect(mergeOlder([], [])).toEqual([]);
    expect(mergeOlder([], [at('a', '2026-08-01T09:00:00.000Z')]).map((m) => m.id)).toEqual(['a']);
  });

  it('does not disturb the arrays it was given', () => {
    const older = [at('a', '2026-08-01T09:00:00.000Z')];
    const recent = [at('b', '2026-08-01T10:00:00.000Z')];
    mergeOlder(older, recent);
    expect(older).toHaveLength(1);
    expect(recent).toHaveLength(1);
  });
});

describe('reachedStart', () => {
  it('is the beginning when the server had fewer than a full page left', () => {
    expect(reachedStart(0, HISTORY_PAGE_SIZE)).toBe(true);
    expect(reachedStart(17, HISTORY_PAGE_SIZE)).toBe(true);
  });

  // A full page means there is probably more; asking again is how we find out.
  it('is not the beginning when the page came back full', () => {
    expect(reachedStart(HISTORY_PAGE_SIZE, HISTORY_PAGE_SIZE)).toBe(false);
  });

  it('pages fifty at a time, matching the server default', () => {
    expect(HISTORY_PAGE_SIZE).toBe(50);
  });
});
