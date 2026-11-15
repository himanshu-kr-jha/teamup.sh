/** Rules about leave, as plain functions. The server checks all of it again. */
import type { LeaveChange, LeavePart, LeaveRecord } from '@/shared/api/client';

/** A date such as 2026-11-18, read as that day and never as an instant. */
function asDay(date: string): Date {
  return new Date(`${date}T12:00:00`);
}

/** Moves a YYYY-MM-DD date by some days. */
export function addDays(date: string, days: number): string {
  const d = asDay(date);
  d.setDate(d.getDate() + days);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** "Wed, Nov 18", with the year only when it is not this year. */
export function dayLabel(date: string, today: string): string {
  const sameYear = date.slice(0, 4) === today.slice(0, 4);
  return asDay(date).toLocaleDateString('en-US', {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    ...(sameYear ? {} : { year: 'numeric' }),
  });
}

const HALF: Record<LeavePart, string> = { full: '', morning: 'morning', afternoon: 'afternoon' };

/** Which days a record covers, in words. */
export function describeLeave(l: Pick<LeaveRecord, 'startsOn' | 'endsOn' | 'part'>, today: string): string {
  const first = dayLabel(l.startsOn, today);
  if (l.part !== 'full') return `${first}, ${HALF[l.part]}`;
  if (l.endsOn === null) return `From ${first}, no return date yet`;
  if (l.endsOn === l.startsOn) return first;
  return `${first} to ${dayLabel(l.endsOn, today)}`;
}

/** How many days a record covers: a half day is a half. Open-ended has none yet. */
export function leaveDays(l: Pick<LeaveRecord, 'startsOn' | 'endsOn' | 'part'>): number | null {
  if (l.part !== 'full') return 0.5;
  if (l.endsOn === null) return null;
  return Math.round((asDay(l.endsOn).getTime() - asDay(l.startsOn).getTime()) / 86_400_000) + 1;
}

/** Still to come or going on now, rather than over. */
export function isCurrentOrUpcoming(l: Pick<LeaveRecord, 'endsOn' | 'startsOn'>, today: string): boolean {
  return l.endsOn === null || l.endsOn >= today;
}

/** Current and upcoming soonest first, then the past most recent first. */
export function splitLeave(records: LeaveRecord[], today: string): { upcoming: LeaveRecord[]; past: LeaveRecord[] } {
  const upcoming = records.filter((l) => isCurrentOrUpcoming(l, today)).sort((a, b) => a.startsOn.localeCompare(b.startsOn));
  const past = records.filter((l) => !isCurrentOrUpcoming(l, today)).sort((a, b) => b.startsOn.localeCompare(a.startsOn));
  return { upcoming, past };
}

export interface LeaveDraft {
  half: boolean;
  startsOn: string;
  /** Empty for no return date yet. */
  endsOn: string;
  part: 'morning' | 'afternoon';
  note: string;
}

/** What stops a draft from being saved, said plainly, or null. */
export function leaveDraftProblem(d: LeaveDraft): string | null {
  if (!d.startsOn) return 'Pick the first day.';
  if (d.half) return null;
  if (d.endsOn && d.endsOn < d.startsOn) return 'The last day cannot be before the first.';
  if (d.endsOn && leaveDays({ startsOn: d.startsOn, endsOn: d.endsOn, part: 'full' })! > 366) {
    return 'One stretch of leave is at most a year. Add another for the rest.';
  }
  if (d.note.trim().length > 200) return 'Keep the note under 200 characters.';
  return null;
}

/** The draft as the server takes it. */
export function leaveDraftBody(d: LeaveDraft): { startsOn: string; endsOn: string | null; part: LeavePart; note: string } {
  if (d.half) return { startsOn: d.startsOn, endsOn: d.startsOn, part: d.part, note: d.note.trim() };
  return { startsOn: d.startsOn, endsOn: d.endsOn || null, part: 'full', note: d.note.trim() };
}

/** A record as a draft, for changing it. */
export function draftFrom(l: LeaveRecord): LeaveDraft {
  return {
    half: l.part !== 'full',
    startsOn: l.startsOn,
    endsOn: l.endsOn ?? '',
    part: l.part === 'afternoon' ? 'afternoon' : 'morning',
    note: l.note,
  };
}

/** Who recorded it and when, and who changed it last if somebody did. */
export function provenance(l: LeaveRecord): string {
  const at = (iso: string) => new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
  const added = `Added by ${l.addedBy?.name ?? 'somebody who has since left'} on ${at(l.addedAt)}`;
  const changed = new Date(l.changedAt).getTime() - new Date(l.addedAt).getTime() > 1000;
  if (!changed) return added;
  return `${added}. Changed by ${l.changedBy?.name ?? 'somebody who has since left'} on ${at(l.changedAt)}`;
}

/**
 * Leave recorded after its first day had already begun. Not wrong, people forget,
 * but worth saying, so a past absence is never quietly turned into leave.
 */
export function recordedAfterTheFact(l: Pick<LeaveRecord, 'startsOn' | 'addedAt'>): boolean {
  return new Date(l.addedAt).getTime() > asDay(l.startsOn).getTime() + 12 * 3_600_000;
}

const ACTION: Record<LeaveChange['action'], string> = { added: 'Added', changed: 'Changed', removed: 'Removed' };

/** One line of a record's history. */
export function describeChange(c: LeaveChange, today: string): string {
  const who = c.actor?.name ?? 'somebody who has since left';
  return `${ACTION[c.action]} by ${who}: ${describeLeave(c, today)}`;
}
