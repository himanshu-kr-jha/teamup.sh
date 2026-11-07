import { describe, expect, it } from 'vitest';
import { lobbyFor, type CallForLobby } from './lobby';

const at = (hour: number, minute = 0) => new Date(2026, 10, 13, hour, minute);
const meeting = (over: Partial<CallForLobby> = {}): CallForLobby => ({
  status: 'scheduled',
  scheduledStart: at(10).toISOString(),
  scheduledEnd: at(11).toISOString(),
  participants: [{ userId: 'me' }],
  ...over,
});

describe('lobbyFor', () => {
  it('stops somebody invited at a meeting that has not opened, whenever it is', () => {
    expect(lobbyFor(meeting(), 'me', at(9, 29))).toBe('early');
    expect(lobbyFor(meeting(), 'me', at(9, 30))).toBe('due');
    expect(lobbyFor(meeting(), 'me', at(10, 30))).toBe('due');
  });

  it('stops them at one that opened early and emptied, while it is still on', () => {
    expect(lobbyFor(meeting({ status: 'ended' }), 'me', at(9))).toBe('early');
    expect(lobbyFor(meeting({ status: 'ended' }), 'me', at(10, 30))).toBe('due');
    expect(lobbyFor(meeting({ status: 'ended' }), 'me', at(11))).toBe('none');
  });

  it('lets them straight into one with people in it', () => {
    expect(lobbyFor(meeting({ status: 'active' }), 'me', at(9))).toBe('none');
  });

  it('sends anybody not invited on to the door, which opens nothing', () => {
    expect(lobbyFor(meeting({ participants: [{ userId: 'other' }] }), 'me', at(9))).toBe('none');
    // Somebody who may not read the call is not told when it is.
    expect(lobbyFor({ status: 'scheduled' }, 'me', at(9))).toBe('none');
  });

  it('is not about an ordinary call or a standing room', () => {
    expect(lobbyFor({ status: 'active', participants: [{ userId: 'me' }] }, 'me', at(9))).toBe('none');
    expect(lobbyFor(meeting({ standingMeetingId: 's' }), 'me', at(9))).toBe('none');
  });
});
