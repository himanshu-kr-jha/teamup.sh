import { describe, expect, it } from 'vitest';
import { createJoinTracker, joinedMessage, CONNECT_SETTLE_MS, REJOIN_QUIET_MS } from './joinPing';

describe('createJoinTracker', () => {
  it('says nothing until the room has connected', () => {
    const tracker = createJoinTracker();
    expect(tracker.joined('a', 0)).toBe(false);
  });

  it('announces somebody who comes in once you are settled', () => {
    const tracker = createJoinTracker();
    tracker.connected([], 0);
    expect(tracker.joined('a', CONNECT_SETTLE_MS + 1)).toBe(true);
  });

  it('does not burst for everybody already there when you connect', () => {
    const tracker = createJoinTracker();
    tracker.connected(['a'], 0);
    // Listed as present, even much later.
    expect(tracker.joined('a', CONNECT_SETTLE_MS + 5000)).toBe(false);
    // Events racing with the connection are the same people.
    expect(tracker.joined('b', 100)).toBe(false);
    expect(tracker.joined('c', CONNECT_SETTLE_MS - 1)).toBe(false);
  });

  it('never announces the same person twice while they stay', () => {
    const tracker = createJoinTracker();
    tracker.connected([], 0);
    expect(tracker.joined('a', 10_000)).toBe(true);
    expect(tracker.joined('a', 10_100)).toBe(false);
  });

  it('stays quiet for somebody whose connection dropped for a moment', () => {
    const tracker = createJoinTracker();
    tracker.connected(['a'], 0);
    tracker.left('a', 10_000);
    expect(tracker.joined('a', 10_000 + REJOIN_QUIET_MS - 1)).toBe(false);
  });

  it('announces somebody coming back after a real absence', () => {
    const tracker = createJoinTracker();
    tracker.connected(['a'], 0);
    tracker.left('a', 10_000);
    expect(tracker.joined('a', 10_000 + REJOIN_QUIET_MS + 1)).toBe(true);
  });

  it('does not treat your own reconnect as everybody arriving', () => {
    const tracker = createJoinTracker();
    tracker.connected(['a'], 0);
    tracker.connected(['a', 'b'], 60_000);
    expect(tracker.joined('b', 60_100)).toBe(false);
    expect(tracker.joined('b', 90_000)).toBe(false);
  });
});

describe('joinedMessage', () => {
  it('names one, two or a few, then counts', () => {
    expect(joinedMessage(['Ann'])).toBe('Ann joined the call');
    expect(joinedMessage(['Ann', 'Bob'])).toBe('Ann and Bob joined the call');
    expect(joinedMessage(['Ann', 'Bob', 'Cy'])).toBe('Ann, Bob and Cy joined the call');
    expect(joinedMessage(['Ann', 'Bob', 'Cy', 'Di', 'Ed'])).toBe('Ann, Bob and 3 others joined the call');
  });
});
