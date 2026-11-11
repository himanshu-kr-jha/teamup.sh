/**
 * How a call that did not happen reads in the conversation, to each side.
 *
 * The server writes one of a few fixed notes (calls.noteFor); the caller and
 * the person called see different words, because what each needs to know is
 * different: the caller knows they called, the other person may not.
 */

export type CallEventKind = 'missed' | 'declined' | 'busy';

export interface CallEventView {
  kind: CallEventKind;
  text: string;
  /** Somebody missed something: shown in red, as a missed call is on a phone. */
  alert: boolean;
}

export function callEventKind(content: string): CallEventKind {
  if (content === 'Call declined') return 'declined';
  if (content === 'Missed call, in another call') return 'busy';
  return 'missed';
}

export function callEventView(content: string, isCaller: boolean, callerName: string): CallEventView {
  const kind = callEventKind(content);
  const who = callerName || 'somebody';
  switch (kind) {
    case 'declined':
      return { kind, alert: false, text: isCaller ? 'Call declined' : `You declined a call from ${who}` };
    case 'busy':
      return {
        kind,
        alert: !isCaller,
        text: isCaller ? 'Busy, in another call or meeting' : `Missed call from ${who} while you were in another call`,
      };
    default:
      return { kind, alert: !isCaller, text: isCaller ? 'No answer' : `Missed call from ${who}` };
  }
}
