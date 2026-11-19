/**
 * Ringing colleagues into a call that is already going: the words, and the list
 * of who is still being rung.
 */

/** How a ring into a call ended, as `call.ring.result` says it. */
export type RingOutcome = 'answered' | 'declined' | 'no_answer' | 'busy' | 'on_leave' | 'cancelled';

/** Somebody being rung into the call you are in. */
export interface Ringing {
  userId: string;
  name: string;
}

/** "Ravi", "Ravi and Sam", "Ravi, Sam and Tom", "Ravi, Sam and 2 others". */
export function namesList(names: string[]): string {
  if (names.length <= 1) return names[0] ?? '';
  if (names.length === 2) return `${names[0]} and ${names[1]}`;
  if (names.length === 3) return `${names[0]}, ${names[1]} and ${names[2]}`;
  return `${names[0]}, ${names[1]} and ${names.length - 2} others`;
}

/** What the incoming ring says under its title. */
export function incomingWording(
  callerName: string,
  { ongoing, with: others = [], standing }: { ongoing?: boolean; with?: string[]; standing?: boolean } = {}
): string {
  if (standing) return `${callerName} is starting now.`;
  if (!ongoing) return `${callerName} is calling.`;
  if (others.length === 0) return `${callerName} is calling you into a call.`;
  return `${callerName} is calling you into a call with ${namesList(others)}.`;
}

/** What everybody in the call is told when a ring ends, or nothing when there is nothing to say. */
export function outcomeMessage(name: string, outcome: RingOutcome): string | null {
  switch (outcome) {
    case 'declined':
      return `${name} declined.`;
    case 'no_answer':
      return `${name} did not answer.`;
    case 'busy':
      return `${name} is in another call or meeting, so was not rung.`;
    case 'on_leave':
      return `${name} is on leave, so was not rung.`;
    default:
      // Picking up is seen when they arrive; a cancel was somebody in the call.
      return null;
  }
}

/** Adds somebody being rung, once. */
export function withRinging(list: Ringing[], person: Ringing): Ringing[] {
  return list.some((p) => p.userId === person.userId) ? list : [...list, person];
}

/** Takes away whoever is no longer being rung. */
export function withoutRinging(list: Ringing[], userIds: Iterable<string>): Ringing[] {
  const gone = new Set(userIds);
  const kept = list.filter((p) => !gone.has(p.userId));
  return kept.length === list.length ? list : kept;
}

/**
 * Whether a call.ended frame about the call you are in means it is over for you.
 * Only the call ending does. Somebody turning down or missing a ring is told and
 * the call goes on, and a ring of yours into it stopping ("ring_cancelled",
 * "ring_no_answer") only takes the ring away: it can arrive just as you come in.
 */
export function endsTheCallForYou(reason?: string): boolean {
  return !reason || reason === 'ended';
}
