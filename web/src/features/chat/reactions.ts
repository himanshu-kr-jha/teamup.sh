/**
 * Reactions on a message.
 *
 * The server has stored these all along and returns them on every message;
 * nothing on this side ever read them or wrote one. The shape is the server's:
 * emoji to the list of people who are on it.
 */
export type ReactionMap = Record<string, string[]>;

/**
 * The emoji offered on the picker.
 *
 * A short list rather than a full picker: these cover almost everything people
 * actually react with, and a grid of two thousand glyphs is a worse way to
 * say "yes" quickly.
 */
export const QUICK_EMOJI = ['👍', '❤️', '😂', '🎉', '👀', '✅'] as const;

/** What `chat.reaction` carries over the socket, and what a click means. */
export interface ReactionEvent {
  emoji: string;
  userId: string;
  action: 'add' | 'remove';
}

/**
 * The same change, applied the same way, whether it came from this person
 * clicking or from somebody else's socket frame.
 *
 * Idempotent on purpose: an optimistic update and a broadcast can both reach
 * the same client, and one person must not read as two.
 */
export function applyReaction(map: ReactionMap | undefined, event: ReactionEvent): ReactionMap {
  const current = map?.[event.emoji] ?? [];
  const next: ReactionMap = { ...map };

  if (event.action === 'add') {
    if (current.includes(event.userId)) return map ? { ...map } : {};
    next[event.emoji] = [...current, event.userId];
    return next;
  }

  const remaining = current.filter((id) => id !== event.userId);
  if (remaining.length === 0) {
    // An emoji nobody is on is not a reaction, and an empty pill under a
    // message is just clutter.
    delete next[event.emoji];
  } else {
    next[event.emoji] = remaining;
  }
  return next;
}

export function hasReacted(
  map: ReactionMap | undefined,
  emoji: string,
  userId: string | undefined
): boolean {
  if (!userId) return false;
  return (map?.[emoji] ?? []).includes(userId);
}

/** One pill under a message. */
export interface ReactionPill {
  emoji: string;
  count: number;
  /** Whether the person reading is on it, which is what makes it a toggle. */
  mine: boolean;
  userIds: string[];
}

/**
 * The pills for a message, most-reacted first.
 *
 * Ordered rather than left in object order so the row does not rearrange
 * itself underneath somebody's cursor as reactions come in.
 */
export function summariseReactions(
  map: ReactionMap | undefined,
  currentUserId: string | undefined
): ReactionPill[] {
  return Object.entries(map ?? {})
    .filter(([, userIds]) => userIds.length > 0)
    .map(([emoji, userIds]) => ({
      emoji,
      count: userIds.length,
      mine: !!currentUserId && userIds.includes(currentUserId),
      userIds,
    }))
    .sort((a, b) => (b.count === a.count ? (a.emoji < b.emoji ? -1 : 1) : b.count - a.count));
}

/** How many people to name before counting the rest. */
const NAMES_SHOWN = 3;

/**
 * Who reacted, for the tooltip on a pill.
 *
 * The payload only ever gives user ids; the names come from the availability
 * list, which covers everybody in the organization. Somebody it cannot name
 * is still counted — losing a reaction because a name lookup missed would be
 * worse than saying "Someone".
 */
export function reactionSummary(
  userIds: string[],
  currentUserId: string | undefined,
  nameOf: (userId: string) => string | undefined
): string {
  if (userIds.length === 0) return '';

  const mine = currentUserId ? userIds.includes(currentUserId) : false;
  const others = userIds.filter((id) => id !== currentUserId).map((id) => nameOf(id) || 'Someone');
  // You first: it is the fastest thing to check about a pill.
  const names = mine ? ['You', ...others] : others;

  const shown = names.slice(0, NAMES_SHOWN);
  const hidden = names.length - shown.length;
  if (hidden > 0) {
    return `${shown.join(', ')} and ${hidden} ${hidden === 1 ? 'other' : 'others'}`;
  }
  if (shown.length === 1) return shown[0];
  return `${shown.slice(0, -1).join(', ')} and ${shown[shown.length - 1]}`;
}
