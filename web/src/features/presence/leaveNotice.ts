import { differenceInCalendarDays, format, parseISO } from 'date-fns';
import type { Person } from './usePresence';

/**
 * When somebody on leave is back, in words: "back tomorrow", "back on
 * Thursday, 19 November", or that there is no date yet. `until` is the server's
 * first day back, a date at midnight UTC; only its date matters.
 */
export function backOnText(until: string | undefined, today: Date = new Date()): string {
  if (!until) return 'with no return date yet';
  const back = parseISO(until.slice(0, 10));
  const days = differenceInCalendarDays(back, today);
  if (days <= 0) return 'back today';
  if (days === 1) return 'back tomorrow';
  const sameYear = back.getFullYear() === today.getFullYear();
  return `back on ${format(back, sameYear ? 'EEEE, d MMMM' : 'EEEE, d MMMM yyyy')}`;
}

/** The person, if they are on leave right now. */
export function onLeave(person: Person | undefined): Person | undefined {
  return person?.status === 'on_leave' ? person : undefined;
}

/** "Ann is on leave, back on Thursday, 19 November." */
export function leaveSentence(person: Person, today: Date = new Date()): string {
  return `${person.name.split(' ')[0]} is on leave, ${backOnText(person.until, today)}.`;
}
