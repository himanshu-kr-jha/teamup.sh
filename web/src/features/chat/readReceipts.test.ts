import { describe, it, expect } from 'vitest';
import { applyReadReceipt } from './readReceipts';
import type { ReadableMessage } from './readReceipts';

const mine = 'me';
const at = (iso: string) => new Date(iso).toISOString();

function msg(id: string, userId: string, createdAt: string, readCount = 0): ReadableMessage {
  return { id, userId, createdAt: at(createdAt), readCount };
}

describe('applyReadReceipt', () => {
  const messages = [
    msg('a', mine, '2026-01-01T10:00:00Z'),
    msg('b', mine, '2026-01-01T10:01:00Z'),
    msg('c', mine, '2026-01-01T10:02:00Z'),
  ];

  it('counts the messages the reader has just crossed', () => {
    const next = applyReadReceipt(messages, {
      channelId: 'ch', userId: 'them', from: null, to: at('2026-01-01T10:01:00Z'),
    }, mine);
    expect(next.map((m) => m.readCount)).toEqual([1, 1, 0]);
  });

  // The reason the receipt carries a window rather than a position. Reading
  // twice must not count the same person twice, which is what "everything at
  // or before the marker" would do on their very next read.
  it('does not count the same reader twice as they read on', () => {
    const first = applyReadReceipt(messages, {
      channelId: 'ch', userId: 'them', from: null, to: at('2026-01-01T10:01:00Z'),
    }, mine);
    const second = applyReadReceipt(first, {
      channelId: 'ch', userId: 'them', from: at('2026-01-01T10:01:00Z'), to: at('2026-01-01T10:02:00Z'),
    }, mine);
    expect(second.map((m) => m.readCount)).toEqual([1, 1, 1]);
  });

  it('adds a second reader to the same messages', () => {
    const one = applyReadReceipt(messages, {
      channelId: 'ch', userId: 'them', from: null, to: at('2026-01-01T10:02:00Z'),
    }, mine);
    const two = applyReadReceipt(one, {
      channelId: 'ch', userId: 'other', from: null, to: at('2026-01-01T10:02:00Z'),
    }, mine);
    expect(two.map((m) => m.readCount)).toEqual([2, 2, 2]);
  });

  // The tick is only ever drawn under your own message, so counting anybody
  // else's is invisible work that would also be wrong: the count is of people
  // other than the author.
  it('leaves other people\'s messages alone', () => {
    const theirs = [msg('x', 'someone-else', '2026-01-01T10:00:00Z')];
    const next = applyReadReceipt(theirs, {
      channelId: 'ch', userId: 'them', from: null, to: at('2026-01-01T10:05:00Z'),
    }, mine);
    expect(next[0].readCount).toBe(0);
  });

  it('never counts a reader reading their own message', () => {
    const ours = [msg('x', mine, '2026-01-01T10:00:00Z')];
    const next = applyReadReceipt(ours, {
      channelId: 'ch', userId: mine, from: null, to: at('2026-01-01T10:05:00Z'),
    }, mine);
    expect(next[0].readCount).toBe(0);
  });

  it('returns the same array when nothing falls in the window', () => {
    const next = applyReadReceipt(messages, {
      channelId: 'ch', userId: 'them', from: at('2026-01-01T09:00:00Z'), to: at('2026-01-01T09:30:00Z'),
    }, mine);
    expect(next).toBe(messages);
  });

  // The window is open at the bottom and closed at the top: a message created
  // exactly at the old marker was already counted.
  it('excludes a message sitting exactly on the previous marker', () => {
    const next = applyReadReceipt(messages, {
      channelId: 'ch', userId: 'them', from: at('2026-01-01T10:00:00Z'), to: at('2026-01-01T10:01:00Z'),
    }, mine);
    expect(next.map((m) => m.readCount)).toEqual([0, 1, 0]);
  });
});
