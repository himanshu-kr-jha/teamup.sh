import { describe, it, expect } from 'vitest';
import { protocol, type Message } from '@/shared/types/protocol';
import { SERVER_PRESENCE_EVENT } from './usePresence';

const frame = (type: Message['type']): Message => ({
  id: 'test',
  type,
  payload: {},
  timestamp: new Date(0).toISOString(),
  version: 1,
});

/**
 * The presence fan-out was encoded and written to every socket in the
 * organization and then thrown away, because this feature subscribed to the
 * name of the *inbound* frame. Nothing failed: the socket accepts a
 * subscription to any type in the union, so the handler simply never ran and
 * presence fell back to polling.
 *
 * `protocol.isPresenceChanged` is the shared statement of what the server
 * broadcasts, so it is what the subscription has to agree with.
 */
describe('the presence subscription', () => {
  it('matches the frame the server broadcasts', () => {
    expect(protocol.isPresenceChanged(frame(SERVER_PRESENCE_EVENT))).toBe(true);
  });

  it('is not the client-to-server update frame, which the server never sends', () => {
    expect(SERVER_PRESENCE_EVENT).not.toBe('presence.update');
  });
});
