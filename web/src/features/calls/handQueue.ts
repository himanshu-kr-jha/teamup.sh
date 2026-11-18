/**
 * Raised hands, worked out from what LiveKit says about the people in a room.
 *
 * The server writes a participant attribute `hand` holding the time the hand went
 * up (internal/calls/hand.go), and removes it when the hand comes down. Everything
 * here is derived from that: nobody's browser keeps its own idea of the queue, so
 * every screen agrees, and somebody who joins late sees the same order.
 */

/** The attribute the server writes; see HandAttribute in internal/calls/hand.go. */
export const HAND_ATTRIBUTE = 'hand';

/** How long your own hand stays up once you are speaking without a break. */
export const AUTO_LOWER_AFTER_MS = 3_000;

/**
 * A pause shorter than this is still the same stretch of speaking: people take a
 * breath, and LiveKit's own "speaking" flag flickers off between words.
 */
export const SPEAKING_GAP_MS = 800;

export interface HandHolder {
  identity: string;
  name?: string;
  attributes?: Readonly<Record<string, string>>;
}

export interface RaisedHand {
  identity: string;
  name: string;
  /** When it went up, in milliseconds. */
  at: number;
  /** 1 for the first hand up. */
  position: number;
}

/** When somebody's hand went up, or null when it is down (or unreadable). */
export function raisedAt(person: HandHolder): number | null {
  const value = person.attributes?.[HAND_ATTRIBUTE];
  if (!value) return null;
  const at = Date.parse(value);
  return Number.isNaN(at) ? null : at;
}

/** Everybody with a hand up, first up first; two at the same moment in a steady order. */
export function handQueue(people: Iterable<HandHolder>): RaisedHand[] {
  const up: Omit<RaisedHand, 'position'>[] = [];
  for (const person of people) {
    const at = raisedAt(person);
    if (at === null) continue;
    up.push({ identity: person.identity, name: person.name || person.identity, at });
  }
  up.sort((a, b) => a.at - b.at || (a.identity < b.identity ? -1 : a.identity > b.identity ? 1 : 0));
  return up.map((hand, i) => ({ ...hand, position: i + 1 }));
}

/** Where somebody is in the queue, from 1, or null with their hand down. */
export function positionOf(queue: RaisedHand[], identity: string): number | null {
  return queue.find((h) => h.identity === identity)?.position ?? null;
}

/**
 * Whose hands went up between two looks at the room: newly raised, or raised again
 * after coming down. Somebody already up is not news, and neither is somebody
 * who was up when you arrived, which is why the first look is taken quietly.
 */
export function newlyRaised(before: RaisedHand[], after: RaisedHand[]): RaisedHand[] {
  const was = new Map(before.map((h) => [h.identity, h.at]));
  return after.filter((h) => was.get(h.identity) !== h.at);
}

/** "1 hand raised", "3 hands raised". */
export function handsLabel(count: number): string {
  return `${count} ${count === 1 ? 'hand' : 'hands'} raised`;
}

export function raisedMessage(name: string): string {
  return `${name} raised their hand`;
}

/** Said to you when your hand comes down because you began to speak. */
export const AUTO_LOWERED_MESSAGE = 'Your hand was lowered because you started speaking.';

/** Said to you when the organizer lowered it. */
export const LOWERED_FOR_YOU_MESSAGE = 'Your hand was lowered.';

/**
 * Watches whether you are speaking and says when your hand should come down:
 * once you have been speaking for a while without a real break. Speaking from
 * before your hand went up does not count; call reset when it goes up.
 */
export function createAutoLower(afterMs = AUTO_LOWER_AFTER_MS, gapMs = SPEAKING_GAP_MS) {
  let since: number | null = null;
  let lastSpoke: number | null = null;
  return {
    /** Tell it what you are doing now; true means it is time to lower your hand. */
    update(speaking: boolean, now: number): boolean {
      if (speaking) {
        if (since === null || lastSpoke === null || now - lastSpoke > gapMs) since = now;
        lastSpoke = now;
        return now - since >= afterMs;
      }
      if (lastSpoke !== null && now - lastSpoke > gapMs) {
        since = null;
        lastSpoke = null;
      }
      return false;
    },
    reset() {
      since = null;
      lastSpoke = null;
    },
  };
}
