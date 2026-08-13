/**
 * Walking backwards through a channel's history.
 *
 * The server returns a page oldest-first and takes a `before` timestamp to
 * page into the past. The client only ever asked for the first page, so
 * anything past the newest fifty messages was unreachable without a database
 * client.
 */

/** Matches the server's own default, so a full page means "probably more". */
export const HISTORY_PAGE_SIZE = 50;

/** The little of a message this file needs to know about. */
export interface HistoryEntry {
  id: string;
  createdAt: string;
  /** Sent but not yet acknowledged: a client clock, not a server row. */
  pending?: boolean;
}

/**
 * The `before` value that asks for the page above what is on screen.
 *
 * Undefined when there is nothing to page back from — an empty channel, or
 * one showing only messages the server has not accepted yet, whose timestamps
 * are this machine's guess and not a position in the history.
 */
export function olderCursor<T extends HistoryEntry>(messages: T[]): string | undefined {
  for (const message of messages) {
    if (!message.pending) return message.createdAt;
  }
  return undefined;
}

/**
 * Puts a freshly fetched older page in front of what is already on screen.
 *
 * `before` is exclusive so the two halves do not normally overlap, but a
 * message can arrive over the socket while a page is in flight and turn up in
 * both. The later copy wins, because it is the one that has been through
 * whatever happened in between.
 */
export function mergeOlder<T extends HistoryEntry>(older: T[], recent: T[]): T[] {
  const byId = new Map<string, T>();
  for (const message of older) byId.set(message.id, message);
  for (const message of recent) byId.set(message.id, message);

  return [...byId.values()].sort((a, b) => {
    const at = new Date(a.createdAt).getTime();
    const bt = new Date(b.createdAt).getTime();
    // Two messages in the same millisecond need *some* order, or the list
    // reshuffles itself on every render.
    return at === bt ? (a.id < b.id ? -1 : a.id > b.id ? 1 : 0) : at - bt;
  });
}

/**
 * Whether that was the last page.
 *
 * A short page means the server ran out, which is how the spinner is told to
 * stop rather than asking for ever at the top of a channel three messages
 * long.
 */
export function reachedStart(pageLength: number, limit: number = HISTORY_PAGE_SIZE): boolean {
  return pageLength < limit;
}
