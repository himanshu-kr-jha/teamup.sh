import { differenceInCalendarDays, format, subDays } from 'date-fns';
import { formatMinutes } from '../callLog';

/** Rules about standing meetings and their attendance, as plain functions. */

export interface AudienceRef {
  id: string;
  name: string;
}

/** Who a standing meeting is for: everybody, or chosen teams (and the teams beneath them) and people. */
export interface Audience {
  everyone: boolean;
  teams: AudienceRef[];
  people: AudienceRef[];
}

export interface StandingMeeting {
  id: string;
  name: string;
  timeOfDay: string;
  timeZone: string;
  weekdays: number[];
  durationMinutes: number;
  createdBy?: string;
  createdAt?: string;
  audience?: Audience;
  /** Whether whoever asked may join it. */
  inAudience?: boolean;
  /** Whether whoever asked may change or archive it. */
  canManage?: boolean;
  /** Whoever joins and never turns their camera on is in review, not present. */
  cameraRequired?: boolean;
  /** Joining counts from this many minutes before the start to this many after. */
  attendBeforeMinutes?: number;
  attendAfterMinutes?: number;
  /** Whether notes are taken in each day's call; left out, the organization's default. */
  takeNotes?: boolean;
}

export type StandingForm = Pick<StandingMeeting, 'name' | 'timeOfDay' | 'timeZone' | 'weekdays' | 'durationMinutes'> & {
  cameraRequired?: boolean;
  takeNotes?: boolean;
  attendBeforeMinutes?: number;
  attendAfterMinutes?: number;
};

/** "08:30 to 09:30": when joining counts, as clock times on the meeting's own clock. */
export function describeWindow(timeOfDay: string, before = 30, after = 30): string {
  const [h, m] = timeOfDay.split(':').map(Number);
  const at = (offset: number) => {
    const total = (((h * 60 + m + offset) % 1440) + 1440) % 1440;
    return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`;
  };
  return `${at(-before)} to ${at(after)}`;
}

/** Who it is for, as the dialog sends it. */
export interface AudienceForm {
  everyone: boolean;
  teamIds: string[];
  personIds: string[];
}

/** What somebody may choose from: the whole organization only for an admin. */
export interface AudienceOptions {
  everyone: boolean;
  teams: AudienceRef[];
  people: AudienceRef[];
}

export function audienceProblem(form: AudienceForm): string | null {
  if (!form.everyone && form.teamIds.length === 0 && form.personIds.length === 0) return 'Choose who it is for.';
  return null;
}

/** "Everybody", or the teams by name and then how many more people. */
export function describeAudience(audience: Audience | undefined): string {
  if (!audience || audience.everyone) return 'Everybody';
  const teams = audience.teams.map((t) => t.name);
  const people = audience.people.length;
  const peopleText = people === 0 ? '' : people === 1 && teams.length === 0 ? audience.people[0].name : `${people} ${people === 1 ? 'person' : 'people'}`;
  const parts = [...teams, ...(peopleText ? [peopleText] : [])];
  if (parts.length === 1) return parts[0];
  return `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`;
}

export type AttendanceStatus = 'present' | 'absent' | 'unknown' | 'on_leave' | 'holiday' | 'field_work' | 'review';

/** Field work that counted for somebody's day: a photo taken in the app, and where. */
export interface AttendanceField {
  id: string;
  takenAt: string;
  latitude: number;
  longitude: number;
  accuracyM: number;
  address?: string;
  note?: string;
}

export type ReviewDecision = 'present' | 'late' | 'absent' | 'on_leave' | 'field_work';

/** A reviewer's decision about somebody's day, and what the day said before it. */
export interface AttendanceReview {
  decision: ReviewDecision;
  lateMinutes?: number;
  note?: string;
  decidedBy: { id: string; name: string } | null;
  decidedAt: string;
  was: AttendanceStatus;
}

/** The leave that excused somebody from a day. */
export interface AttendanceLeave {
  part: 'full' | 'morning' | 'afternoon';
  recordedAt: string;
  recordedBy: { id: string; name: string } | null;
  /** Recorded only once the meeting had already started. */
  afterTheDay: boolean;
}

export interface AttendancePerson {
  userId: string;
  name: string;
  status: AttendanceStatus;
  firstJoinAt: string | null;
  lastLeaveAt: string | null;
  minutesPresent: number;
  minutesLate: number;
  leave?: AttendanceLeave;
  /** On a day that required cameras: whether they turned theirs on. */
  cameraOn?: boolean;
  field?: AttendanceField;
  review?: AttendanceReview;
  /** Somebody who came only after attendance closed: absent, but they did come. */
  joinedAfterWindow?: string;
}

export interface AttendanceDay {
  date: string;
  callId: string | null;
  scheduledStart: string | null;
  attendanceRecorded: boolean;
  /** Whether whoever is reading may open this day's call log. */
  canReadLog: boolean;
  /** The organization's holiday on this date, if it is one. */
  holiday?: string;
  /** The stand-up's camera rule as it was that day. */
  cameraRequired?: boolean;
  /** When joining counted that day. */
  attendFrom?: string;
  attendUntil?: string;
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

/**
 * Whether two zone names are the same zone. A browser may report the old name
 * of a zone (Chrome says Asia/Calcutta for India) where the meeting was saved
 * with the current one (Asia/Kolkata), so names are compared as the browser
 * resolves them.
 */
export function sameZone(a: string, b: string): boolean {
  if (a === b) return true;
  const resolve = (zone: string) => {
    try {
      return new Intl.DateTimeFormat('en', { timeZone: zone }).resolvedOptions().timeZone;
    } catch {
      return zone;
    }
  };
  return resolve(a) === resolve(b);
}

/** The zone is said only when it is not the one you are in. */
export function describeSchedule(
  meeting: Pick<StandingMeeting, 'weekdays' | 'timeOfDay' | 'timeZone' | 'durationMinutes'>,
  yourZone: string = browserTimeZone()
): string {
  const zone = sameZone(meeting.timeZone, yourZone) ? '' : ` ${meeting.timeZone}`;
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
  const counts: Record<AttendanceStatus, number> = {
    present: 0,
    absent: 0,
    unknown: 0,
    on_leave: 0,
    holiday: 0,
    field_work: 0,
    review: 0,
  };
  for (const person of people) counts[person.status]++;
  return counts;
}

/** A day's tally in words, leaving out what is nought, except present. */
export function attendanceSummary(day: Pick<AttendanceDay, 'attendanceRecorded' | 'holiday' | 'people'>): string {
  const counts = attendanceCounts(day.people);
  if (day.holiday && counts.present === 0) return 'Nobody was expected';
  const parts = [`${counts.present} present`];
  if (counts.absent > 0 || (!day.holiday && counts.on_leave === 0 && counts.unknown === 0 && counts.field_work === 0 && counts.review === 0))
    parts.push(`${counts.absent} absent`);
  if (counts.field_work > 0) parts.push(`${counts.field_work} in the field`);
  if (counts.review > 0) parts.push(`${counts.review} in review`);
  if (counts.on_leave > 0) parts.push(`${counts.on_leave} on leave`);
  if (counts.unknown > 0) parts.push(`${counts.unknown} unknown`);
  return parts.join(', ');
}

// In review first among the rest, since somebody has to act on it.
const STATUS_RANK: Record<AttendanceStatus, number> = {
  review: 0,
  present: 1,
  field_work: 2,
  unknown: 3,
  absent: 4,
  on_leave: 5,
  holiday: 6,
};

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
