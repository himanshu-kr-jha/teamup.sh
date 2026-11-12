import { describe, expect, it } from 'vitest';
import { MEETING_CHAT_TOPIC, chatFromPacket, mergeChat, startsGroup, unreadCount } from './meetingChat';
import type { MeetingChatMessage } from '@/shared/api/client';

const msg = (id: string, senderId: string, createdAt: string, content = 'hi'): MeetingChatMessage => ({
  id,
  callId: 'c',
  senderId,
  senderName: senderId,
  guest: false,
  content,
  createdAt,
});

const packet = (m: unknown) => new TextEncoder().encode(JSON.stringify(m));

describe('meeting chat', () => {
  it('takes chat only from the server, on its own topic', () => {
    const m = msg('1', 'ann', '2026-11-12T10:00:00Z');
    expect(chatFromPacket(packet(m), undefined, MEETING_CHAT_TOPIC)).toEqual(m);
    expect(chatFromPacket(packet(m), { identity: 'mallory' }, MEETING_CHAT_TOPIC)).toBeNull();
    expect(chatFromPacket(packet(m), undefined, 'something-else')).toBeNull();
    expect(chatFromPacket(new TextEncoder().encode('not json'), undefined, MEETING_CHAT_TOPIC)).toBeNull();
    expect(chatFromPacket(packet({ id: '1' }), undefined, MEETING_CHAT_TOPIC)).toBeNull();
  });

  it('keeps each message once, in the order it was written', () => {
    const a = msg('a', 'ann', '2026-11-12T10:00:00Z');
    const b = msg('b', 'ben', '2026-11-12T10:01:00Z');
    const c = msg('c', 'ann', '2026-11-12T10:02:00Z');
    const known = [a, c];
    expect(mergeChat(known, [b, a])).toEqual([a, b, c]);
    expect(mergeChat(known, [a])).toBe(known);
  });

  it('orders by the moment, however the time is written', () => {
    const early = msg('z', 'ann', '2026-11-12T12:09:00.123456+05:30');
    const late = msg('a', 'pat', '2026-11-12T06:40:00.123456789Z');
    expect(mergeChat([late], [early]).map((m) => m.id)).toEqual(['z', 'a']);
  });

  it('counts what other people wrote that has not been seen', () => {
    const list = [msg('a', 'ann', '1'), msg('b', 'me', '2'), msg('c', 'ben', '3')];
    expect(unreadCount(list, new Set(['a']), 'me')).toBe(1);
    expect(unreadCount(list, new Set(), 'me')).toBe(2);
  });

  it('names the sender again for somebody else, or after a pause', () => {
    const a = msg('a', 'ann', '2026-11-12T10:00:00Z');
    expect(startsGroup(undefined, a)).toBe(true);
    expect(startsGroup(a, msg('b', 'ann', '2026-11-12T10:01:00Z'))).toBe(false);
    expect(startsGroup(a, msg('b', 'ben', '2026-11-12T10:01:00Z'))).toBe(true);
    expect(startsGroup(a, msg('b', 'ann', '2026-11-12T10:06:00Z'))).toBe(true);
  });
});
