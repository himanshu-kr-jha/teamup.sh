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
 * once you have been speaking for a while without a real break.
 *
 * Only speaking that starts after the hand went up counts. Somebody who raises
 * their hand while still finishing a sentence is asking to speak next, so it
 * waits for a real pause (as long as a breath that ends a stretch) before it
 * starts to count. Make a new one, or call reset, when the hand goes up.
 */
export function createAutoLower(afterMs = AUTO_LOWER_AFTER_MS, gapMs = SPEAKING_GAP_MS) {
  let since: number | null = null;
  let lastSpoke: number | null = null;
  // When the quiet began that lets it start counting; null while not yet quiet.
  let quietFrom: number | null = null;
  let armed = false;
  return {
    /** Tell it what you are doing now; true means it is time to lower your hand. */
    update(speaking: boolean, now: number): boolean {
      if (!armed) {
        if (speaking) {
          quietFrom = null;
          return false;
        }
        if (quietFrom === null) quietFrom = now;
        if (now - quietFrom >= gapMs) armed = true;
        return false;
      }
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
      quietFrom = null;
      armed = false;
    },
  };
}

/**
 * How long to wait before each try at lowering your hand again, when the server
 * could not be reached: soon, then less and less often, and then give up and say so.
 */
export const LOWER_RETRY_DELAYS_MS = [1_000, 2_000, 4_000, 8_000, 16_000];

/** Said when your hand could not be lowered by itself, after every retry. */
export const AUTO_LOWER_FAILED_MESSAGE = 'Your hand is still up: TeamUp could not lower it. Lower it yourself when you can.';

/** Said when a hand lost to a reconnection went back up, but at the back of the line. */
export const RESTORED_AS_NEW_MESSAGE = 'The call reconnected. Your hand is up again, at the back of the line.';

/** Said when a hand lost to a reconnection could not be put back. */
export const NOT_RESTORED_MESSAGE = 'The call reconnected and your hand came down. Raise it again if you still want to speak.';

/** How often the browser holding a raised hand asks for a fresh proof of its place. */
export const PROOF_REFRESH_MS = 60_000;

/**
 * After the connection comes back: whether your hand needs putting back up. It
 * does when you had one up, nobody took it down, and the room has lost it.
 */
export function needsRestore(kept: { raisedAt: string } | null, attributeNow: string | undefined): boolean {
  return !!kept && !attributeNow;
}
