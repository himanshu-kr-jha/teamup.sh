import { differenceInCalendarDays, format, subDays } from 'date-fns';
import { formatMinutes } from '../callLog';

/** Rules about standing meetings and their attendance, as plain functions. */

export interface StandingMeeting {
  id: string;
  name: string;
  timeOfDay: string;
  timeZone: string;
  weekdays: number[];
  durationMinutes: number;
  createdBy?: string;
  createdAt?: string;
}

export type StandingForm = Pick<StandingMeeting, 'name' | 'timeOfDay' | 'timeZone' | 'weekdays' | 'durationMinutes'>;

export type AttendanceStatus = 'present' | 'absent' | 'unknown';

export interface AttendancePerson {
  userId: string;
  name: string;
  status: AttendanceStatus;
  firstJoinAt: string | null;
  lastLeaveAt: string | null;
  minutesPresent: number;
  minutesLate: number;
}

export interface AttendanceDay {
  date: string;
  callId: string | null;
  scheduledStart: string | null;
  attendanceRecorded: boolean;
  /** Whether whoever is reading may open this day's call log. */
  canReadLog: boolean;
  people: AttendancePerson[];
}

const DAY_NAMES = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

export const WEEKDAY_OPTIONS = DAY_NAMES.map((label, index) => ({ value: index + 1, label }));

export function describeWeekdays(weekdays: number[]): string {
  const days = [...new Set(weekdays)].sort((a, b) => a - b);
  if (days.length === 0) return 'No days';
  if (days.length === 7) return 'Every day';
  const unbroken = days.every((day, i) => i === 0 || day === days[i - 1] + 1);
  if (unbroken && days.length >= 3) {
    return `${DAY_NAMES[days[0] - 1]} to ${DAY_NAMES[days[days.length - 1] - 1]}`;
  }
  return days.map((day) => DAY_NAMES[day - 1]).join(', ');
}

export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

/** The zone is said only when it is not the one you are in. */
export function describeSchedule(
  meeting: Pick<StandingMeeting, 'weekdays' | 'timeOfDay' | 'timeZone' | 'durationMinutes'>,
  yourZone: string = browserTimeZone()
): string {
  const zone = meeting.timeZone === yourZone ? '' : ` ${meeting.timeZone}`;
  return `${describeWeekdays(meeting.weekdays)} at ${meeting.timeOfDay}${zone}, ${formatMinutes(meeting.durationMinutes)}`;
}

export function standingFormProblem(form: StandingForm): string | null {
  if (!form.name.trim()) return 'Give it a name.';
  if (!form.timeOfDay) return 'Pick a time of day.';
  if (form.weekdays.length === 0) return 'Pick at least one day.';
  if (!Number.isFinite(form.durationMinutes) || form.durationMinutes < 1) return 'The length must be at least 1 minute (in minutes).';
  if (form.durationMinutes > 480) return 'The length can be at most 480 minutes.';
  return null;
}

export function attendanceCounts(people: AttendancePerson[]): Record<AttendanceStatus, number> {
  const counts: Record<AttendanceStatus, number> = { present: 0, absent: 0, unknown: 0 };
  for (const person of people) counts[person.status]++;
  return counts;
}

const STATUS_RANK: Record<AttendanceStatus, number> = { present: 0, unknown: 1, absent: 2 };

export function orderAttendance(people: AttendancePerson[]): AttendancePerson[] {
  return [...people].sort((a, b) => STATUS_RANK[a.status] - STATUS_RANK[b.status] || a.name.localeCompare(b.name));
}

export interface AttendanceRange {
  from: string;
  to: string;
}

/** The calendar date at this moment in a zone, as YYYY-MM-DD. */
export function dateInZone(now: Date, timeZone: string): string {
  // en-CA writes dates as YYYY-MM-DD.
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(now);
}

/**
 * The last week, ending today where the meeting is, which is not always today where
 * you are: a scrum in Kolkata is already on tomorrow for a manager in New York.
 */
export function defaultAttendanceRange(now: Date = new Date(), timeZone?: string): AttendanceRange {
  if (!timeZone) return { from: format(subDays(now, 6), 'yyyy-MM-dd'), to: format(now, 'yyyy-MM-dd') };
  const to = dateInZone(now, timeZone);
  // At noon, so that the arithmetic cannot land on the wrong side of a clock change.
  return { from: format(subDays(new Date(`${to}T12:00:00`), 6), 'yyyy-MM-dd'), to };
}

/** The server allows 92 days; saying so here beats a failed request. */
export function attendanceRangeProblem(range: AttendanceRange): string | null {
  if (range.from > range.to) return 'The start date must be on or before the end date.';
  const days = differenceInCalendarDays(new Date(`${range.to}T00:00:00`), new Date(`${range.from}T00:00:00`));
  if (days > 92) return 'Pick a range of at most 92 days.';
  return null;
}
