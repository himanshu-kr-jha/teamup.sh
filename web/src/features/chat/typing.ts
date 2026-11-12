import { useCallback, useEffect, useRef, useState } from 'react';
import { useWebSocket } from '@/features/websocket/WSProvider';

/**
 * "Alice is typing…" in a conversation.
 *
 * Whoever types says so over the socket now and then rather than on every key,
 * and says when they stop. The server passes it to the rest of the
 * conversation with the typist's id, which it sets itself. A typist who goes
 * quiet without saying so (a closed tab, a dropped connection) is forgotten
 * after a few seconds, because nothing else would ever clear them.
 */

/** How often, at most, typing is announced while it goes on. */
export const TYPING_EVERY_MS = 3_000;
/** A pause this long is the end of typing. */
export const TYPING_IDLE_MS = 5_000;
/** Somebody not heard from for this long is no longer shown, whatever they last said. */
export const TYPING_FORGET_MS = 7_000;

/** The words under the message box, for the people typing; empty for nobody. */
export function typingText(names: string[]): string {
  if (names.length === 0) return '';
  if (names.length === 1) return `${names[0]} is typing…`;
  if (names.length === 2) return `${names[0]} and ${names[1]} are typing…`;
  const others = names.length - 2;
  return `${names[0]}, ${names[1]} and ${others} ${others === 1 ? 'other' : 'others'} are typing…`;
}

type Typists = Record<string, Record<string, number>>; // channel → person → when they are forgotten

/** Who is typing in each conversation, after one frame. */
export function applyTyping(
  all: Typists,
  frame: { channelId?: string; userId?: string; isTyping?: boolean },
  now: number
): Typists {
  const { channelId, userId } = frame;
  if (!channelId || !userId) return all;
  const here = { ...(all[channelId] ?? {}) };
  if (frame.isTyping) here[userId] = now + TYPING_FORGET_MS;
  else if (userId in here) delete here[userId];
  else return all;
  return { ...all, [channelId]: here };
}

/** Everybody not heard from in time taken off, or the same object if nobody was. */
export function forgetQuiet(all: Typists, now: number): Typists {
  let changed = false;
  const next: Typists = {};
  for (const [channel, people] of Object.entries(all)) {
    const kept: Record<string, number> = {};
    for (const [id, until] of Object.entries(people)) {
      if (until > now) kept[id] = until;
      else changed = true;
    }
    if (Object.keys(kept).length > 0) next[channel] = kept;
  }
  return changed ? next : all;
}

/** The ids of the people typing in a conversation, other than you. */
export function useTypingIn(channelId: string | null | undefined, me: string | undefined): string[] {
  const { on } = useWebSocket();
  const [typists, setTypists] = useState<Typists>({});

  useEffect(() => {
    const stopTyping = on('chat.typing', (msg) => {
      setTypists((all) => applyTyping(all, (msg.payload ?? {}) as Record<string, never>, Date.now()));
    });
    // A message is the end of its author's typing; there is no need to wait.
    const stopOnMessage = on('chat.message', (msg) => {
      const m = ((msg.payload as { message?: unknown })?.message ?? msg.payload) as { channelId?: string; userId?: string };
      setTypists((all) => applyTyping(all, { channelId: m?.channelId, userId: m?.userId, isTyping: false }, Date.now()));
    });
    return () => {
      stopTyping();
      stopOnMessage();
    };
  }, [on]);

  const anybody = Object.keys(typists).length > 0;
  useEffect(() => {
    if (!anybody) return;
    const sweep = setInterval(() => setTypists((all) => forgetQuiet(all, Date.now())), 1_000);
    return () => clearInterval(sweep);
  }, [anybody]);

  if (!channelId) return [];
  return Object.keys(typists[channelId] ?? {}).filter((id) => id !== me);
}

/**
 * Tells the conversation you are typing. Call typed with the box's text on
 * every change, and stopped when you send or leave it.
 */
export function useTypingAnnouncer(channelId: string | null | undefined) {
  const { send, state } = useWebSocket();
  const lastSaid = useRef(0);
  const saidIn = useRef<string | null>(null);
  const idle = useRef<ReturnType<typeof setTimeout>>();

  const say = useCallback(
    (channel: string, isTyping: boolean) => {
      if (state !== 'ready') return;
      send({ type: 'chat.typing', payload: { channelId: channel, isTyping } });
    },
    [send, state]
  );

  const stopped = useCallback(() => {
    clearTimeout(idle.current);
    if (saidIn.current) say(saidIn.current, false);
    saidIn.current = null;
    lastSaid.current = 0;
  }, [say]);

  const typed = useCallback(
    (text: string) => {
      if (!channelId) return;
      if (!text.trim()) {
        stopped();
        return;
      }
      const now = Date.now();
      if (saidIn.current !== channelId || now - lastSaid.current >= TYPING_EVERY_MS) {
        if (saidIn.current && saidIn.current !== channelId) say(saidIn.current, false);
        say(channelId, true);
        saidIn.current = channelId;
        lastSaid.current = now;
      }
      clearTimeout(idle.current);
      idle.current = setTimeout(stopped, TYPING_IDLE_MS);
    },
    [channelId, say, stopped]
  );

  // Moving to another conversation, or away from chat, is stopping.
  useEffect(() => stopped, [channelId, stopped]);

  return { typed, stopped };
}
