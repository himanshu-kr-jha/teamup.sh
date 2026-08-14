/**
 * Threads.
 *
 * `replyTo` has been stored on every message and round-tripped through the API
 * since the beginning, and nothing ever read it: a reply was recorded and then
 * rendered inline like anything else, which makes the relationship invisible.
 *
 * A thread here is one level deep, the way it is in Slack. The server settles
 * that on the way in — a reply to a reply is re-pointed at the thread root
 * before it is stored — so on this side `replyTo` IS the thread's identity,
 * and no walking is needed to find out which conversation a message is part
 * of. Everything below leans on that invariant, and `partitionThreads` is
 * written so that a message which somehow breaks it still cannot end up
 * rendered in the middle of the channel.
 */

import { mergeOlder, type HistoryEntry } from './history';

/** The little of a message this file needs to know about. */
export interface ThreadEntry extends HistoryEntry {
  /** The thread root this message is a reply to; absent on a root. */
  replyTo?: string;
  /** How many replies hang off this message. Only ever set on a root. */
  replyCount?: number;
  /** When the thread last moved, so "3 replies" can say how long ago. */
  lastReplyAt?: string;
}

/** Whether this message belongs inside a thread rather than the timeline. */
export function isThreadReply(message: ThreadEntry): boolean {
  return !!message.replyTo;
}

/**
 * Which thread a message belongs to.
 *
 * A root is the head of its own thread, which is what makes "open the thread
 * on this message" one operation rather than two.
 */
export function threadIdOf(message: ThreadEntry): string {
  return message.replyTo ?? message.id;
}

/**
 * Splits a page of history into what the channel shows and what belongs in a
 * thread.
 *
 * The server already leaves replies out of the timeline, so this normally
 * finds none — it is here for the two cases where that is not true: rows
 * written before threads existed, and the window during a deploy when a
 * client is newer or older than the server it is talking to. In both, a reply
 * rendered inline is precisely the defect threading exists to remove, so the
 * client refuses to render one rather than trusting that it will never see it.
 */
export function partitionThreads<T extends ThreadEntry>(
  messages: T[]
): { timeline: T[]; repliesByRoot: Record<string, T[]> } {
  const timeline: T[] = [];
  const repliesByRoot: Record<string, T[]> = {};

  for (const message of messages) {
    const parent = message.replyTo;
    if (!parent) {
      timeline.push(message);
      continue;
    }
    (repliesByRoot[parent] ??= []).push(message);
  }
  return { timeline, repliesByRoot };
}

/**
 * Records that a thread grew, on the parent in the channel timeline.
 *
 * A reply arrives over the same `chat.message` broadcast as anything else, and
 * the only thing the channel view has to do about it is move a number. The
 * caller is responsible for not counting the same reply twice — ChatPage keeps
 * the ids it has already counted — because this function cannot tell one frame
 * from a repeat of it.
 *
 * The same array comes back untouched when the parent is not on screen, which
 * happens whenever the thread is older than the page being shown: there is no
 * number to move, React has no re-render to do, and the count is right again
 * as soon as that part of the history is read.
 */
export function bumpReplyCount<T extends ThreadEntry>(messages: T[], rootId: string, at: string): T[] {
  let found = false;
  const next = messages.map((message) => {
    if (message.id !== rootId) return message;
    found = true;
    return {
      ...message,
      replyCount: (message.replyCount ?? 0) + 1,
      // Frames can arrive out of order, and a thread that reads as having gone
      // quiet at the moment it did not is worse than one whose clock is coarse.
      lastReplyAt:
        message.lastReplyAt && message.lastReplyAt > at ? message.lastReplyAt : at,
    };
  });
  return found ? next : messages;
}

/**
 * The replies of one thread, from however many places they arrived.
 *
 * Deliberately the same merge the channel history uses: dedupe by id, order by
 * (createdAt, id), later copy wins. A reply can land in the fetched thread and
 * over the socket at once, and one person saying one thing has to read as one
 * message wherever it came from.
 */
export const mergeReplies = mergeOlder;

/** "3 replies", for the way into a thread. */
export function replyCountLabel(count: number): string {
  return `${count} ${count === 1 ? 'reply' : 'replies'}`;
}
