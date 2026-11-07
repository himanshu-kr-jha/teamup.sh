import { JOIN_EARLY_MINUTES, meetingStillOn } from '@/features/calendar/layout';

/** What a page needs to know of a call to decide whether to walk in. */
export interface CallForLobby {
  status?: string;
  scheduledStart?: string;
  scheduledEnd?: string;
  standingMeetingId?: string;
  participants?: { userId: string }[];
}

export type LobbyKind = 'none' | 'early' | 'due';

/**
 * Whether following a link to this call should stop at a lobby rather than join.
 *
 * Asking for a token is what opens a scheduled meeting, and a meeting that has opened
 * can no longer be moved (decisions 22 and 35), so merely opening its page must not
 * ask. Only somebody who could join it is stopped: anybody else asking for a token
 * meets the door, which opens nothing. A meeting with people in it is simply joined,
 * and an occurrence is not locked by anything.
 */
export function lobbyFor(call: CallForLobby, userId: string | undefined, now: Date): LobbyKind {
  if (!call.scheduledStart || call.standingMeetingId) return 'none';
  if (call.status !== 'scheduled' && !meetingStillOn(call, now)) return 'none';
  if (!call.participants?.some((p) => p.userId === userId)) return 'none';
  const opens = new Date(call.scheduledStart).getTime() - JOIN_EARLY_MINUTES * 60_000;
  return now.getTime() < opens ? 'early' : 'due';
}
