/** The audit trail of a call, as `GET /calls/:id/log` answers it. */
export type CallEventType =
  | 'knocked'
  | 'admitted'
  | 'denied'
  | 'joined'
  | 'left'
  | 'screen_share_started'
  | 'screen_share_stopped';

export interface CallEvent {
  id: string;
  type: CallEventType;
  userId: string;
  userName?: string;
  actorId?: string;
  actorName?: string;
  at: string;
}

export interface CallSession {
  userId: string;
  userName?: string;
  joinedAt: string | null;
  leftAt: string | null;
}

export interface CallLog {
  events: CallEvent[];
  sessions: CallSession[];
  attendanceRecorded: boolean;
}

export function describeEvent(event: CallEvent): string {
  const who = event.userName?.trim() || 'Somebody';
  const by = event.actorName?.trim() || 'Somebody';
  switch (event.type) {
    case 'knocked':
      return `${who} asked to join`;
    case 'admitted':
      return `${by} let ${who} in`;
    case 'denied':
      return `${by} turned ${who} away`;
    case 'joined':
      return `${who} joined`;
    case 'left':
      return `${who} left`;
    case 'screen_share_started':
      return `${who} started sharing their screen`;
    case 'screen_share_stopped':
      return `${who} stopped sharing their screen`;
    default:
      return `${who}: ${event.type}`;
  }
}

const STEP: Record<string, number> = {
  knocked: 0,
  admitted: 1,
  denied: 1,
  joined: 2,
  screen_share_started: 3,
  screen_share_stopped: 4,
  left: 5,
};

/**
 * In the order things happened. LiveKit reports whole seconds while TeamUp's own
 * events carry fractions, so within one second somebody could read as joining
 * before they were let in. Within a second the sequence of steps decides.
 */
export function orderEvents(events: CallEvent[]): CallEvent[] {
  const second = (event: CallEvent) => Math.floor(new Date(event.at).getTime() / 1000);
  return [...events].sort(
    (a, b) => second(a) - second(b) || (STEP[a.type] ?? 9) - (STEP[b.type] ?? 9)
  );
}

export interface CallLogPerson {
  userId: string;
  name: string;
  sessions: number;
  firstJoinAt: string | null;
  /** Null while they are still connected. */
  lastLeaveAt: string | null;
  stillHere: boolean;
  minutes: number;
}

/**
 * One line per person from however many sessions they had. Time on two devices at
 * once counts once, and somebody still connected counts up to now.
 */
export function peopleFromSessions(sessions: CallSession[], now: Date = new Date()): CallLogPerson[] {
  const byPerson = new Map<string, CallSession[]>();
  for (const session of sessions) {
    byPerson.set(session.userId, [...(byPerson.get(session.userId) ?? []), session]);
  }

  const people: CallLogPerson[] = [];
  for (const [userId, own] of byPerson) {
    const joins = own.map((s) => s.joinedAt).filter((t): t is string => !!t).sort();
    const leaves = own.map((s) => s.leftAt).filter((t): t is string => !!t).sort();
    const stillHere = own.some((s) => s.joinedAt && !s.leftAt);

    const spans = own
      .filter((s) => s.joinedAt)
      .map((s) => [new Date(s.joinedAt!).getTime(), s.leftAt ? new Date(s.leftAt).getTime() : now.getTime()] as const)
      .sort((a, b) => a[0] - b[0]);
    let totalMs = 0;
    let coveredTo = -Infinity;
    for (const [start, end] of spans) {
      const from = Math.max(start, coveredTo);
      if (end > from) totalMs += end - from;
      coveredTo = Math.max(coveredTo, end);
    }

    people.push({
      userId,
      name: own.find((s) => s.userName)?.userName ?? 'Somebody',
      sessions: own.length,
      firstJoinAt: joins[0] ?? null,
      lastLeaveAt: stillHere ? null : (leaves[leaves.length - 1] ?? null),
      stillHere,
      minutes: Math.round(totalMs / 60_000),
    });
  }
  return people;
}

export function formatMinutes(minutes: number): string {
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} h` : `${hours} h ${rest} min`;
}
