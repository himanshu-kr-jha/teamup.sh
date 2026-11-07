/**
 * The tick under your own message.
 *
 * `readCount` is how many other people in the channel have read a message. It
 * is counted when history is fetched, from a watermark per member — so until
 * the server began announcing that a watermark had moved, the tick could only
 * change by reloading the page. Somebody reading your message while you are
 * looking at it is exactly the moment the tick is worth anything.
 *
 * The receipt carries the window the reader's marker moved through rather than
 * where it landed, and that is the whole trick. A count must not be advanced
 * twice for the same person, and a client holding only a number cannot tell
 * whether the reader it has just heard about was already in it. Crossing a
 * message happens exactly once per reader, so counting only what falls inside
 * the window is right the first time and every time after.
 */

/** The little of a message this file needs to know about. */
export interface ReadableMessage {
  id: string;
  /** Who wrote it. The tick is only drawn under your own. */
  userId: string;
  createdAt: string;
  readCount?: number;
}

export interface ReadReceipt {
  channelId: string;
  userId: string;
  /** The reader's previous marker; null the first time they read here. */
  from: string | null;
  to: string;
}

/**
 * Adds this reader to every message of yours that their marker has just
 * crossed.
 *
 * Returns the array unchanged when the window covers nothing, so React sees no
 * new object and a channel full of people reading old history does not rerender
 * the conversation on every receipt.
 */
export function applyReadReceipt<T extends ReadableMessage>(
  messages: T[],
  receipt: ReadReceipt,
  currentUserId: string
): T[] {
  // Somebody reading their own message tells you nothing: the count is of
  // other people, and the server counts it that way too.
  if (receipt.userId === currentUserId) return messages;

  const to = Date.parse(receipt.to);
  if (Number.isNaN(to)) return messages;
  // No previous marker means the window opens at the beginning of time.
  const fromParsed = receipt.from === null ? NaN : Date.parse(receipt.from);
  const from = Number.isNaN(fromParsed) ? -Infinity : fromParsed;

  let touched = false;
  const next = messages.map((m) => {
    if (m.userId !== currentUserId) return m;
    const at = Date.parse(m.createdAt);
    // Open at the bottom: a message sitting exactly on the old marker was
    // already counted when the reader crossed it the first time.
    if (Number.isNaN(at) || at <= from || at > to) return m;
    touched = true;
    return { ...m, readCount: (m.readCount ?? 0) + 1 };
  });

  return touched ? next : messages;
}
