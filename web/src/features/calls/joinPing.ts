/**
 * Deciding when somebody coming into a call is news.
 *
 * LiveKit says when a participant connects, and that is immediate and does not
 * depend on webhooks. It is not all news, though: everybody already in the room
 * when you connect arrives at once, and a connection that blips for a second is
 * somebody leaving and coming straight back.
 */

/** Anything heard this soon after connecting is the room you just walked into. */
export const CONNECT_SETTLE_MS = 2_000;
/** Coming back this soon was a dropped connection and not an arrival. */
export const REJOIN_QUIET_MS = 15_000;
/** Arrivals this close together are one announcement. */
export const BURST_WINDOW_MS = 800;

export interface JoinTracker {
  /** You connected or reconnected, and these people were already there. */
  connected: (present: string[], now: number) => void;
  /** True when this arrival should be announced. */
  joined: (identity: string, now: number) => boolean;
  left: (identity: string, now: number) => void;
}

export function createJoinTracker(): JoinTracker {
  let settledAt: number | null = null;
  const here = new Set<string>();
  const leftAt = new Map<string, number>();

  return {
    connected(present, now) {
      settledAt = now + CONNECT_SETTLE_MS;
      present.forEach((identity) => here.add(identity));
    },
    joined(identity, now) {
      if (settledAt === null) return false;
      const already = here.has(identity);
      here.add(identity);
      if (already || now < settledAt) return false;
      const gone = leftAt.get(identity);
      leftAt.delete(identity);
      return gone === undefined || now - gone > REJOIN_QUIET_MS;
    },
    left(identity, now) {
      here.delete(identity);
      leftAt.set(identity, now);
    },
  };
}

/** Names a few people, and counts the rest. */
export function joinedMessage(names: string[]): string {
  if (names.length === 1) return `${names[0]} joined the call`;
  if (names.length <= 3) {
    return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]} joined the call`;
  }
  return `${names[0]}, ${names[1]} and ${names.length - 2} others joined the call`;
}
