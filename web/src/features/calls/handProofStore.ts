/**
 * Your raised hand's proof of its place, kept in this tab's sessionStorage so a
 * reload does not lose it.
 *
 * A reload is a new connection to the room, and a new connection has no hand on
 * it. What is kept is only what the server gave for the hand: its time and the
 * proof it signed (internal/calls/hand.go), with when that proof stops being
 * good. Never the LiveKit token or anything else that lets somebody in. It is
 * per tab, per call and per person, and goes when the hand comes down, the call
 * ends, you leave, or it is too old to be any use.
 */

/** How long the server takes a proof (handProofTTL in internal/calls/hand.go). */
export const HAND_PROOF_TTL_MS = 3 * 60_000;

const PREFIX = 'teamup.hand.';

export interface StoredHand {
  raisedAt: string;
  proof: string;
  /** When the server will stop taking the proof, in milliseconds. */
  expiresAt: number;
}

/** The storage this tab has, or none (private mode, or no window). */
export function tabStorage(): Storage | null {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

const keyOf = (callId: string, userId: string) => `${PREFIX}${callId}.${userId}`;

/** When a proof was signed, from its "seconds.mac" form; null if it is not one. */
export function proofIssuedAt(proof: string): number | null {
  const secs = Number(proof.split('.')[0]);
  return Number.isFinite(secs) && secs > 0 && proof.includes('.') ? secs * 1000 : null;
}

/** Keeps a hand's proof for this call and person, replacing any older one. */
export function saveHand(storage: Storage | null, callId: string, userId: string, hand: { raisedAt: string; proof?: string }) {
  if (!storage || !callId || !userId || !hand.proof) return;
  const issued = proofIssuedAt(hand.proof);
  if (issued === null) return;
  const stored: StoredHand = { raisedAt: hand.raisedAt, proof: hand.proof, expiresAt: issued + HAND_PROOF_TTL_MS };
  try {
    storage.setItem(keyOf(callId, userId), JSON.stringify(stored));
  } catch {
    // Full or refused: a reload will simply not bring the hand back.
  }
}

/** The kept hand for this call and person, if there is one still good; an old or broken one is removed. */
export function loadHand(storage: Storage | null, callId: string, userId: string, now = Date.now()): StoredHand | null {
  if (!storage || !callId || !userId) return null;
  const key = keyOf(callId, userId);
  let stored: StoredHand | null = null;
  try {
    const raw = storage.getItem(key);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<StoredHand>;
    if (typeof parsed.raisedAt === 'string' && typeof parsed.proof === 'string' && typeof parsed.expiresAt === 'number') {
      stored = { raisedAt: parsed.raisedAt, proof: parsed.proof, expiresAt: parsed.expiresAt };
    }
  } catch {
    stored = null;
  }
  if (!stored || stored.expiresAt <= now) {
    forgetHand(storage, callId, userId);
    return null;
  }
  return stored;
}

/** Forgets the kept hand for this call and person. */
export function forgetHand(storage: Storage | null, callId: string, userId: string) {
  if (!storage) return;
  try {
    storage.removeItem(keyOf(callId, userId));
  } catch {
    // Nothing to forget.
  }
}

/** Forgets every kept hand for a call, whoever's: the call is over, or you left it. */
export function forgetCallHands(storage: Storage | null, callId: string) {
  if (!storage || !callId) return;
  try {
    const mine: string[] = [];
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (key?.startsWith(`${PREFIX}${callId}.`)) mine.push(key);
    }
    mine.forEach((key) => storage.removeItem(key));
  } catch {
    // Nothing to forget.
  }
}
