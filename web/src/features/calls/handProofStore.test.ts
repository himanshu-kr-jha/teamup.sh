import { describe, expect, it } from 'vitest';
import { HAND_PROOF_TTL_MS, forgetCallHands, forgetHand, loadHand, proofIssuedAt, saveHand } from './handProofStore';

/** sessionStorage, in memory. */
function memory(): Storage {
  const items = new Map<string, string>();
  return {
    get length() {
      return items.size;
    },
    key: (i) => [...items.keys()][i] ?? null,
    getItem: (k) => items.get(k) ?? null,
    setItem: (k, v) => void items.set(k, String(v)),
    removeItem: (k) => void items.delete(k),
    clear: () => items.clear(),
  };
}

const ISSUED = 1_795_000_000; // seconds
const hand = { raisedAt: '2026-11-18T09:30:00.000Z', proof: `${ISSUED}.signature` };
const at = (msAfterIssue: number) => ISSUED * 1000 + msAfterIssue;

describe('a raised hand kept through a reload', () => {
  it('comes back for the same call and person while its proof is good', () => {
    const s = memory();
    saveHand(s, 'call-1', 'sam', hand);
    expect(loadHand(s, 'call-1', 'sam', at(1_000))).toEqual({ ...hand, expiresAt: at(HAND_PROOF_TTL_MS) });
    expect(loadHand(s, 'call-2', 'sam', at(1_000))).toBeNull();
    expect(loadHand(s, 'call-1', 'olivia', at(1_000))).toBeNull();
  });

  it('is gone once the server would no longer take its proof', () => {
    const s = memory();
    saveHand(s, 'call-1', 'sam', hand);
    expect(loadHand(s, 'call-1', 'sam', at(HAND_PROOF_TTL_MS))).toBeNull();
    // And it is removed, not just ignored.
    expect(s.length).toBe(0);
  });

  it('keeps the newest proof', () => {
    const s = memory();
    saveHand(s, 'call-1', 'sam', hand);
    saveHand(s, 'call-1', 'sam', { raisedAt: hand.raisedAt, proof: `${ISSUED + 60}.newer` });
    const kept = loadHand(s, 'call-1', 'sam', at(HAND_PROOF_TTL_MS + 1_000));
    expect(kept?.proof).toBe(`${ISSUED + 60}.newer`);
  });

  it('keeps nothing without a proof, and only what the server gave', () => {
    const s = memory();
    saveHand(s, 'call-1', 'sam', { raisedAt: hand.raisedAt });
    saveHand(s, 'call-1', 'sam', { raisedAt: hand.raisedAt, proof: 'nonsense' });
    expect(s.length).toBe(0);
    saveHand(s, 'call-1', 'sam', hand);
    expect(Object.keys(JSON.parse(s.getItem(s.key(0)!)!)).sort()).toEqual(['expiresAt', 'proof', 'raisedAt']);
  });

  it('is forgotten when the hand comes down, and for everybody when the call is left', () => {
    const s = memory();
    saveHand(s, 'call-1', 'sam', hand);
    forgetHand(s, 'call-1', 'sam');
    expect(loadHand(s, 'call-1', 'sam', at(1_000))).toBeNull();

    saveHand(s, 'call-1', 'sam', hand);
    saveHand(s, 'call-1', 'olivia', hand);
    saveHand(s, 'call-10', 'sam', hand);
    forgetCallHands(s, 'call-1');
    expect(loadHand(s, 'call-1', 'sam', at(1_000))).toBeNull();
    expect(loadHand(s, 'call-1', 'olivia', at(1_000))).toBeNull();
    // Another call whose id starts the same way is left alone.
    expect(loadHand(s, 'call-10', 'sam', at(1_000))).not.toBeNull();
  });

  it('survives storage that is broken or missing', () => {
    const s = memory();
    s.setItem('teamup.hand.call-1.sam', '{not json');
    expect(loadHand(s, 'call-1', 'sam', at(0))).toBeNull();
    expect(s.length).toBe(0);
    expect(loadHand(null, 'call-1', 'sam')).toBeNull();
    expect(() => saveHand(null, 'call-1', 'sam', hand)).not.toThrow();
    const refusing = { ...memory(), setItem: () => { throw new Error('quota'); } } as Storage;
    expect(() => saveHand(refusing, 'call-1', 'sam', hand)).not.toThrow();
  });

  it('reads when a proof was signed', () => {
    expect(proofIssuedAt(`${ISSUED}.x`)).toBe(ISSUED * 1000);
    expect(proofIssuedAt('x.y')).toBeNull();
    expect(proofIssuedAt('123')).toBeNull();
  });
});
