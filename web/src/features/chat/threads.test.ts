import { describe, it, expect } from 'vitest';
import {
  bumpReplyCount,
  isThreadReply,
  mergeReplies,
  partitionThreads,
  replyCountLabel,
  threadIdOf,
} from './threads';

const msg = (id: string, createdAt: string, extra: Record<string, unknown> = {}) => ({
  id,
  createdAt,
  ...extra,
});

describe('threadIdOf', () => {
  // The server flattens threads to one level on the way in, so reply_to always
  // points at the root. That is what lets the client work out which thread a
  // message belongs to without asking.
  it('is the parent for a reply and the message itself for a root', () => {
    expect(threadIdOf(msg('a', '2026-08-01T10:00:00.000Z'))).toBe('a');
    expect(threadIdOf(msg('b', '2026-08-01T10:00:00.000Z', { replyTo: 'a' }))).toBe('a');
  });

  it('knows a reply from a root', () => {
    expect(isThreadReply(msg('a', '2026-08-01T10:00:00.000Z'))).toBe(false);
    expect(isThreadReply(msg('b', '2026-08-01T10:00:00.000Z', { replyTo: 'a' }))).toBe(true);
  });
});

describe('partitionThreads', () => {
  it('keeps replies out of the timeline and files them under their root', () => {
    const messages = [
      msg('a', '2026-08-01T10:00:00.000Z'),
      msg('b', '2026-08-01T10:01:00.000Z', { replyTo: 'a' }),
      msg('c', '2026-08-01T10:02:00.000Z'),
      msg('d', '2026-08-01T10:03:00.000Z', { replyTo: 'a' }),
    ];
    const { timeline, repliesByRoot } = partitionThreads(messages);
    expect(timeline.map((m) => m.id)).toEqual(['a', 'c']);
    expect(repliesByRoot['a'].map((m) => m.id)).toEqual(['b', 'd']);
    expect(repliesByRoot['c']).toBeUndefined();
  });

  // The server leaves replies out of the timeline, so this should normally
  // find none. It runs anyway because "normally" covers neither the rows
  // written before threads existed nor a socket frame arriving while the two
  // sides of a deploy disagree — and a reply rendered inline is the exact
  // defect threading is here to fix.
  it('leaves a timeline that has no replies in it alone', () => {
    const messages = [msg('a', '2026-08-01T10:00:00.000Z'), msg('c', '2026-08-01T10:02:00.000Z')];
    const { timeline, repliesByRoot } = partitionThreads(messages);
    expect(timeline.map((m) => m.id)).toEqual(['a', 'c']);
    expect(Object.keys(repliesByRoot)).toEqual([]);
  });

  it('copes with a reply whose root is not on this page', () => {
    const messages = [msg('b', '2026-08-01T10:01:00.000Z', { replyTo: 'somewhere-older' })];
    const { timeline, repliesByRoot } = partitionThreads(messages);
    expect(timeline).toEqual([]);
    expect(repliesByRoot['somewhere-older'].map((m) => m.id)).toEqual(['b']);
  });

  it('does not disturb the array it was given', () => {
    const messages = [msg('a', '2026-08-01T10:00:00.000Z')];
    partitionThreads(messages);
    expect(messages).toHaveLength(1);
  });
});

describe('bumpReplyCount', () => {
  it('moves the parent count on by one and records when', () => {
    const timeline = [
      msg('a', '2026-08-01T10:00:00.000Z', { replyCount: 2 }),
      msg('c', '2026-08-01T10:02:00.000Z', { replyCount: 0 }),
    ];
    const next = bumpReplyCount(timeline, 'a', '2026-08-01T11:00:00.000Z');
    expect(next[0]).toMatchObject({ replyCount: 3, lastReplyAt: '2026-08-01T11:00:00.000Z' });
    expect(next[1]).toMatchObject({ replyCount: 0 });
  });

  it('starts a count on a message that had no thread yet', () => {
    const timeline = [msg('a', '2026-08-01T10:00:00.000Z')];
    expect(bumpReplyCount(timeline, 'a', '2026-08-01T11:00:00.000Z')[0]).toMatchObject({
      replyCount: 1,
    });
  });

  // The parent may be older than the fifty messages on screen. Returning the
  // same array means React has no re-render to do, and the count is right
  // again as soon as that part of the history is read.
  it('gives back exactly what it was given when the parent is not on screen', () => {
    const timeline = [msg('a', '2026-08-01T10:00:00.000Z', { replyCount: 1 })];
    expect(bumpReplyCount(timeline, 'not-here', '2026-08-01T11:00:00.000Z')).toBe(timeline);
  });

  // An out-of-order frame must not drag the "last reply" backwards, or a
  // thread reads as having gone quiet at the moment it did not.
  it('never moves the last reply time backwards', () => {
    const timeline = [
      msg('a', '2026-08-01T10:00:00.000Z', {
        replyCount: 1,
        lastReplyAt: '2026-08-01T12:00:00.000Z',
      }),
    ];
    const next = bumpReplyCount(timeline, 'a', '2026-08-01T11:00:00.000Z');
    expect(next[0]).toMatchObject({ replyCount: 2, lastReplyAt: '2026-08-01T12:00:00.000Z' });
  });

  it('does not disturb the array it was given', () => {
    const timeline = [msg('a', '2026-08-01T10:00:00.000Z', { replyCount: 1 })];
    bumpReplyCount(timeline, 'a', '2026-08-01T11:00:00.000Z');
    expect(timeline[0]).toMatchObject({ replyCount: 1 });
  });
});

describe('mergeReplies', () => {
  it('reads oldest-first however the fetched and live halves interleave', () => {
    const fetched = [
      msg('b', '2026-08-01T10:01:00.000Z', { replyTo: 'a' }),
      msg('d', '2026-08-01T10:03:00.000Z', { replyTo: 'a' }),
    ];
    const live = [msg('c', '2026-08-01T10:02:00.000Z', { replyTo: 'a' })];
    expect(mergeReplies(fetched, live).map((m) => m.id)).toEqual(['b', 'c', 'd']);
  });

  // A reply can arrive over the socket while the thread is being fetched and
  // land in both halves. One person saying one thing is one message.
  it('shows a reply that arrived twice once', () => {
    const fetched = [msg('b', '2026-08-01T10:01:00.000Z', { replyTo: 'a' })];
    const live = [msg('b', '2026-08-01T10:01:00.000Z', { replyTo: 'a', content: 'edited' })];
    const merged = mergeReplies(fetched, live);
    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({ content: 'edited' });
  });
});

describe('replyCountLabel', () => {
  it('counts in English', () => {
    expect(replyCountLabel(1)).toBe('1 reply');
    expect(replyCountLabel(2)).toBe('2 replies');
    expect(replyCountLabel(11)).toBe('11 replies');
  });

  // Nothing should render the affordance at all in this case, but a label that
  // reads "0 replies" if something does is better than one that reads "1 reply".
  it('says nothing about a thread that does not exist', () => {
    expect(replyCountLabel(0)).toBe('0 replies');
  });
});
