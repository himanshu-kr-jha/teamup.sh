import type { MeetingChatMessage } from '@/shared/api/client';

/** The LiveKit data topic a meeting's chat travels on (internal/calls). */
export const MEETING_CHAT_TOPIC = 'teamup.meeting-chat';

/** The longest message the server takes, in characters. */
export const MAX_CHAT_MESSAGE = 2000;

/**
 * A chat message from a packet in the room, or null for anything else. Only the
 * server delivers chat: a packet another participant published has a sender,
 * and is ignored, so nobody can put words in somebody else's name.
 */
export function chatFromPacket(
  payload: Uint8Array,
  from: { identity: string } | undefined,
  topic: string | undefined
): MeetingChatMessage | null {
  if (topic !== MEETING_CHAT_TOPIC || from) return null;
  try {
    const m = JSON.parse(new TextDecoder().decode(payload)) as Partial<MeetingChatMessage>;
    if (typeof m.id !== 'string' || typeof m.content !== 'string' || typeof m.senderId !== 'string' || typeof m.createdAt !== 'string') {
      return null;
    }
    return {
      id: m.id,
      callId: String(m.callId ?? ''),
      senderId: m.senderId,
      senderName: String(m.senderName ?? ''),
      guest: !!m.guest,
      content: m.content,
      createdAt: m.createdAt,
    };
  } catch {
    return null;
  }
}

/** Messages in the order they were written, each once, whichever way it arrived. */
export function mergeChat(known: MeetingChatMessage[], arrived: MeetingChatMessage[]): MeetingChatMessage[] {
  if (arrived.length === 0) return known;
  const byId = new Map(known.map((m) => [m.id, m]));
  let changed = false;
  for (const m of arrived) {
    if (!byId.has(m.id)) {
      byId.set(m.id, m);
      changed = true;
    }
  }
  if (!changed) return known;
  // By the moment, not the text: the same moment can be written more than one way.
  return [...byId.values()].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt) || a.id.localeCompare(b.id));
}

/** How many messages from other people came after the ones already seen. */
export function unreadCount(messages: MeetingChatMessage[], seen: ReadonlySet<string>, me: string): number {
  return messages.filter((m) => m.senderId !== me && !seen.has(m.id)).length;
}

/**
 * Whether a message starts a new group: a different sender, or the same one
 * after a pause, which is when it is worth saying who and when again.
 */
export function startsGroup(prev: MeetingChatMessage | undefined, m: MeetingChatMessage): boolean {
  if (!prev || prev.senderId !== m.senderId) return true;
  return new Date(m.createdAt).getTime() - new Date(prev.createdAt).getTime() > 5 * 60_000;
}
