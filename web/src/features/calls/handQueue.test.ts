import { describe, expect, it } from 'vitest';
import {
  AUTO_LOWER_AFTER_MS,
  SPEAKING_GAP_MS,
  createAutoLower,
  handQueue,
  handsLabel,
  newlyRaised,
  positionOf,
  raisedAt,
} from './handQueue';

const person = (identity: string, hand?: string, name = identity) => ({
  identity,
  name,
  attributes: hand === undefined ? {} : { hand },
});

describe('the queue of raised hands', () => {
  it('puts the first hand up first, whoever joined first', () => {
    const queue = handQueue([
      person('ben', '2026-11-18T09:30:20.000Z', 'Ben'),
      person('olga'),
      person('asha', '2026-11-18T09:30:15.250Z', 'Asha'),
      person('cara', '2026-11-18T09:31:00Z', 'Cara'),
    ]);
    expect(queue.map((h) => [h.name, h.position])).toEqual([
      ['Asha', 1],
      ['Ben', 2],
      ['Cara', 3],
    ]);
    expect(positionOf(queue, 'ben')).toBe(2);
    expect(positionOf(queue, 'olga')).toBeNull();
  });

  it('orders two hands raised at the same moment the same way on every screen', () => {
    const same = '2026-11-18T09:30:15.000Z';
    const one = handQueue([person('b', same), person('a', same)]);
    const other = handQueue([person('a', same), person('b', same)]);
    expect(one.map((h) => h.identity)).toEqual(['a', 'b']);
    expect(other.map((h) => h.identity)).toEqual(['a', 'b']);
  });

  it('ignores a hand that is down or that it cannot read', () => {
    expect(raisedAt(person('a', ''))).toBeNull();
    expect(raisedAt(person('a', 'soon'))).toBeNull();
    expect(raisedAt({ identity: 'a' })).toBeNull();
    expect(handQueue([person('a', ''), person('b', 'soon'), person('c')])).toEqual([]);
  });

  it('falls back to the identity for somebody with no name', () => {
    expect(handQueue([{ identity: 'x', attributes: { hand: '2026-11-18T09:30:15Z' } }])[0].name).toBe('x');
  });

  it('counts hands in words', () => {
    expect(handsLabel(1)).toBe('1 hand raised');
    expect(handsLabel(2)).toBe('2 hands raised');
  });

  it('tells you about hands that went up, not ones already up or that came down', () => {
    const before = handQueue([person('a', '2026-11-18T09:30:00Z'), person('b', '2026-11-18T09:30:05Z')]);
    const after = handQueue([
      person('a', '2026-11-18T09:30:00Z'),
      person('c', '2026-11-18T09:30:10Z'),
    ]);
    expect(newlyRaised(before, after).map((h) => h.identity)).toEqual(['c']);
    // Down and up again is news again.
    const again = handQueue([person('a', '2026-11-18T09:31:00Z')]);
    expect(newlyRaised(after, again).map((h) => h.identity)).toEqual(['a']);
  });
});

describe('lowering your hand once you speak', () => {
  /** Ticks every 250ms through a list of [from, to, speaking] stretches; returns when it said lower. */
  function run(stretches: [number, number, boolean][]) {
    const auto = createAutoLower();
    for (const [from, to, speaking] of stretches) {
      for (let t = from; t <= to; t += 250) {
        if (auto.update(speaking, t)) return t;
      }
    }
    return null;
  }

  it('lowers it after about three seconds of speaking', () => {
    expect(run([[0, 10_000, true]])).toBe(AUTO_LOWER_AFTER_MS);
  });

  it('does not lower it for a few words', () => {
    expect(
      run([
        [0, 1_500, true],
        [1_750, 4_000, false],
        [4_250, 6_000, true],
        [6_250, 9_000, false],
      ])
    ).toBeNull();
  });

  it('counts speaking through a short breath as one stretch', () => {
    const breath = SPEAKING_GAP_MS - 300;
    expect(
      run([
        [0, 2_000, true],
        [2_250, 2_000 + breath, false],
        [2_250 + breath, 10_000, true],
      ])
    ).toBe(AUTO_LOWER_AFTER_MS);
  });

  it('starts counting again after a real pause', () => {
    expect(
      run([
        [0, 2_500, true],
        [2_750, 4_000, false],
        [4_250, 10_000, true],
      ])
    ).toBe(4_250 + AUTO_LOWER_AFTER_MS);
  });

  it('does not count speaking from before the hand went up', () => {
    const auto = createAutoLower();
    auto.update(true, 0);
    auto.update(true, 2_500);
    auto.reset();
    for (let t = 2_750; t < 5_750; t += 250) expect(auto.update(true, t)).toBe(false);
    expect(auto.update(true, 5_750)).toBe(true);
  });
});
