import { addDays, addMinutes, format, isSameDay, startOfDay, startOfWeek } from 'date-fns';

/**
 * The calendar's arithmetic. Dates are local to the browser, because that is the
 * zone the page is drawn in. Positions are wall clock minutes rather than elapsed
 * ones, so an hour line says the time it says on the day the clocks change.
 */

export type CalendarKind = 'busy' | 'focus' | 'open';
export type CalendarSource = 'block' | 'meeting' | 'call' | 'standing';

/**
 * One thing on somebody's calendar. Without authority over that person the server
 * sends only kind and times, so everything else is optional and never inferred.
 */
export interface CalendarItem {
  kind: CalendarKind;
  startsAt: string;
  endsAt: string;
  id?: string;
  source?: CalendarSource;
  title?: string;
  callId?: string;
  standingMeetingId?: string;
  organizerId?: string;
  inviteeIds?: string[];
  status?: string;
  /** Whether whoever is reading may open this call's log. */
  canReadLog?: boolean;
}

export type CalendarView = 'week' | 'day';

export const MINUTES_IN_DAY = 24 * 60;
/** An event is drawn at least this tall, in minutes, so its name can be read. */
export const MIN_EVENT_MINUTES = 20;
/** How early a meeting offers Join. Opening one locks it against being moved. */
export const JOIN_EARLY_MINUTES = 30;

export function visibleDays(anchor: Date, view: CalendarView): Date[] {
  if (view === 'day') return [startOfDay(anchor)];
  const monday = startOfWeek(anchor, { weekStartsOn: 1 });
  return Array.from({ length: 7 }, (_, i) => addDays(monday, i));
}

export function rangeForDays(days: Date[]): { from: string; to: string } {
  return {
    from: startOfDay(days[0]).toISOString(),
    to: addDays(startOfDay(days[days.length - 1]), 1).toISOString(),
  };
}

export function shiftDays(anchor: Date, view: CalendarView, direction: number): Date {
  return addDays(anchor, direction * (view === 'week' ? 7 : 1));
}

export function dayLabelKey(date: Date): string {
  return format(date, 'yyyy-MM-dd');
}

export interface Segment {
  item: CalendarItem;
  index: number;
  startMinute: number;
  endMinute: number;
  startsBeforeDay: boolean;
  endsAfterDay: boolean;
}

function minuteOfDay(date: Date): number {
  return date.getHours() * 60 + date.getMinutes();
}

/** The part of each item that falls on this day, so a long one is cut at midnight. */
export function segmentsForDay(items: CalendarItem[], day: Date): Segment[] {
  const dayStart = startOfDay(day).getTime();
  const dayEnd = addDays(startOfDay(day), 1).getTime();
  const segments: Segment[] = [];
  items.forEach((item, index) => {
    const start = new Date(item.startsAt);
    const end = new Date(item.endsAt);
    const instant = start.getTime() === end.getTime();
    const onThisDay = instant
      ? start.getTime() >= dayStart && start.getTime() < dayEnd
      : start.getTime() < dayEnd && end.getTime() > dayStart;
    if (!onThisDay) return;
    const startsBeforeDay = start.getTime() < dayStart;
    const endsAfterDay = end.getTime() > dayEnd;
    const startMinute = startsBeforeDay ? 0 : minuteOfDay(start);
    const endMinute = end.getTime() >= dayEnd ? MINUTES_IN_DAY : minuteOfDay(end);
    segments.push({
      item,
      index,
      startMinute,
      // The clocks going back can put the wall clock end before the start.
      endMinute: Math.max(endMinute, startMinute + 1),
      startsBeforeDay,
      endsAfterDay,
    });
  });
  return segments;
}

export interface PlacedSegment extends Segment {
  column: number;
  columns: number;
  /** Something behind it, an open slot, needs a strip left showing. */
  inset: boolean;
}

export interface OverflowChip {
  startMinute: number;
  endMinute: number;
  column: number;
  columns: number;
  segments: Segment[];
}

export interface DayLayout {
  background: Segment[];
  events: PlacedSegment[];
  overflow: OverflowChip[];
}

const visibleEnd = (segment: Segment) => Math.max(segment.endMinute, segment.startMinute + MIN_EVENT_MINUTES);

/**
 * Arranges one day. Open slots go behind everything, since a meeting inside one is
 * the whole point of it. Everything else that overlaps sits in columns, and when a
 * pile-up needs more columns than can be read, the rest go into a chip saying how
 * many, which opens a list.
 */
export function layoutDay(segments: Segment[], { maxColumns = 2 }: { maxColumns?: number } = {}): DayLayout {
  const background = segments.filter((s) => s.item.kind === 'open');
  const foreground = segments
    .filter((s) => s.item.kind !== 'open')
    .sort((a, b) => a.startMinute - b.startMinute || b.endMinute - a.endMinute);

  const clusters: Segment[][] = [];
  let reach = -1;
  for (const segment of foreground) {
    if (segment.startMinute >= reach) clusters.push([]);
    clusters[clusters.length - 1].push(segment);
    reach = Math.max(reach, visibleEnd(segment));
  }

  const events: PlacedSegment[] = [];
  const overflow: OverflowChip[] = [];
  for (const cluster of clusters) {
    const columnEnds: number[] = [];
    const placed = cluster.map((segment) => {
      let column = columnEnds.findIndex((end) => end <= segment.startMinute);
      if (column === -1) column = columnEnds.length;
      columnEnds[column] = visibleEnd(segment);
      return { segment, column };
    });
    const needed = columnEnds.length;
    const columns = Math.min(needed, maxColumns);
    const hiding = needed > maxColumns;
    const hidden: Segment[] = [];
    for (const { segment, column } of placed) {
      if (hiding && column >= maxColumns - 1) {
        hidden.push(segment);
      } else {
        const inset = background.some(
          (b) => b.startMinute < visibleEnd(segment) && segment.startMinute < b.endMinute
        );
        events.push({ ...segment, column, columns, inset });
      }
    }
    if (hidden.length > 0) {
      overflow.push({
        startMinute: Math.min(...hidden.map((s) => s.startMinute)),
        endMinute: Math.max(...hidden.map(visibleEnd)),
        column: maxColumns - 1,
        columns,
        segments: hidden,
      });
    }
  }
  return { background, events, overflow };
}

export function hasOverlap(aStart: Date, aEnd: Date, bStart: Date, bEnd: Date): boolean {
  return aStart.getTime() < bEnd.getTime() && bStart.getTime() < aEnd.getTime();
}

const KIND_LABELS: Record<CalendarKind, string> = {
  busy: 'Busy',
  focus: 'Focus time',
  open: 'Open for meetings',
};

/** The title if the server sent one. Anything else is only what the kind says. */
export function itemLabel(item: CalendarItem): string {
  const title = item.title?.trim();
  if (title) return title;
  if (item.source === 'call') return 'Call';
  return KIND_LABELS[item.kind];
}

export function kindLabel(kind: CalendarKind): string {
  return KIND_LABELS[kind];
}

/** Items seen without authority have no id, so the position has to tell them apart. */
export function itemKey(item: CalendarItem, index: number): string {
  return item.id ?? `${item.kind}-${item.startsAt}-${item.endsAt}-${index}`;
}

export function nowOffset(now: Date, day: Date): number | null {
  return isSameDay(now, day) ? minuteOfDay(now) : null;
}

export type JoinTarget = { type: 'call'; callId: string } | { type: 'standing'; standingMeetingId: string };

/** What Join on this item would do right now, or null if it should not be offered. */
/**
 * A scheduled meeting whose room emptied before its end is not over. It comes back
 * until then (decision 23), so somebody who looked in at 09:40 has not finished a
 * meeting everybody is expecting at 10:00.
 */
export function meetingStillOn(
  call: { status?: string; scheduledEnd?: string; standingMeetingId?: string },
  now: Date
): boolean {
  return (
    call.status === 'ended' &&
    !call.standingMeetingId &&
    !!call.scheduledEnd &&
    now.getTime() < new Date(call.scheduledEnd).getTime()
  );
}

export function joinTarget(item: CalendarItem, now: Date): JoinTarget | null {
  const start = new Date(item.startsAt).getTime();
  const end = new Date(item.endsAt).getTime();
  const early = start - JOIN_EARLY_MINUTES * 60_000;
  const time = now.getTime();

  if (item.source === 'standing' && item.standingMeetingId) {
    // The room stays usable for the rest of its day (decision 23), or to the end of
    // a slot that runs past midnight.
    const dayOver = Math.max(addDays(startOfDay(new Date(item.startsAt)), 1).getTime(), end);
    return time >= early && time < dayOver ? { type: 'standing', standingMeetingId: item.standingMeetingId } : null;
  }
  if (!item.callId) return null;
  if (item.source === 'call') {
    return item.status === 'active' ? { type: 'call', callId: item.callId } : null;
  }
  if (item.source === 'meeting') {
    // An ended one is back on the calendar only until its end, which it is not past
    // while it is still shown at its scheduled time.
    const live = item.status === 'scheduled' || item.status === 'active' || item.status === 'ended';
    return live && time >= early && time < end ? { type: 'call', callId: item.callId } : null;
  }
  return null;
}

export type Availability = 'free' | 'open' | 'focus' | 'busy';

const RANK: Record<Availability, number> = { free: 0, open: 1, focus: 2, busy: 3 };

export interface AvailabilityPerson {
  userId: string;
  items: Pick<CalendarItem, 'kind' | 'startsAt' | 'endsAt'>[];
}

type Slot = Pick<CalendarItem, 'startsAt' | 'endsAt'>;

/** The most limiting thing somebody has during this time. */
export function availabilityAt(items: AvailabilityPerson['items'], start: Date, end: Date, ignore?: Slot): Availability {
  let answer: Availability = 'free';
  for (const item of items) {
    if (ignore && item.kind === 'busy' && sameInstant(item.startsAt, ignore.startsAt) && sameInstant(item.endsAt, ignore.endsAt)) continue;
    if (!hasOverlap(start, end, new Date(item.startsAt), new Date(item.endsAt))) continue;
    if (RANK[item.kind] > RANK[answer]) answer = item.kind;
  }
  return answer;
}

function sameInstant(a: string, b: string): boolean {
  return new Date(a).getTime() === new Date(b).getTime();
}

export interface PersonConflict {
  userId: string;
  state: 'busy' | 'focus';
}

/**
 * Who would be double booked. `ignore` is the time of the meeting being edited,
 * which is on everybody's calendar and would otherwise conflict with itself.
 */
export function conflictsAt(people: AvailabilityPerson[], start: Date, end: Date, ignore?: Slot): PersonConflict[] {
  const conflicts: PersonConflict[] = [];
  for (const person of people) {
    const state = availabilityAt(person.items, start, end, ignore);
    if (state === 'busy' || state === 'focus') conflicts.push({ userId: person.userId, state });
  }
  return conflicts;
}

/** What a datetime-local input holds. */
export function toInputValue(date: Date): string {
  return format(date, "yyyy-MM-dd'T'HH:mm");
}

export function fromInputValue(value: string): Date | null {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

const MAX_BLOCK_MS = 30 * 24 * 60 * 60_000;
const MAX_MEETING_MS = 24 * 60 * 60_000;
export const MAX_INVITEES = 200;

function timesProblem(start: Date | null, end: Date | null): string | null {
  if (!start) return 'Pick a start time.';
  if (!end) return 'Pick an end time.';
  if (end.getTime() <= start.getTime()) return 'The end must be after the start.';
  return null;
}

export function blockProblem(start: Date | null, end: Date | null): string | null {
  const problem = timesProblem(start, end);
  if (problem) return problem;
  if (end!.getTime() - start!.getTime() >= MAX_BLOCK_MS) return 'A block must be shorter than 30 days.';
  return null;
}

export function meetingProblem(title: string, start: Date | null, end: Date | null, invited: number): string | null {
  if (!title.trim()) return 'Give the meeting a name.';
  const problem = timesProblem(start, end);
  if (problem) return problem;
  if (end!.getTime() - start!.getTime() > MAX_MEETING_MS) return 'A meeting can last at most 24 hours.';
  if (invited > MAX_INVITEES) return `A meeting can have at most ${MAX_INVITEES} people invited.`;
  return null;
}

export function defaultSlot(now: Date): { start: Date; end: Date } {
  const past = now.getMinutes() % 30;
  const start = addMinutes(now, past === 0 ? 30 : 30 - past);
  start.setSeconds(0, 0);
  return { start, end: addMinutes(start, 30) };
}

/** The wall clock time this many minutes into a day. */
export function minuteToDate(day: Date, minute: number): Date {
  const date = startOfDay(day);
  date.setHours(Math.floor(minute / 60), minute % 60, 0, 0);
  return date;
}

/** A half hour slot from where somebody clicked in a day column. */
export function slotFromOffset(day: Date, minute: number): { start: Date; end: Date } {
  const rounded = Math.min(Math.floor(minute / 30) * 30, MINUTES_IN_DAY - 30);
  const start = minuteToDate(day, rounded);
  return { start, end: addMinutes(start, 30) };
}

/** The hours a small availability bar covers: the working day, or more if the time asked needs it. */
export function timelineWindow(start: Date, end: Date): { from: Date; to: Date } {
  const from = startOfDay(start);
  from.setHours(Math.min(7, Math.max(start.getHours() - 1, 0)), 0, 0, 0);
  const to = startOfDay(start);
  const lastHour = isSameDay(start, end) ? end.getHours() + 1 : 24;
  to.setHours(Math.min(24, Math.max(20, lastHour)), 0, 0, 0);
  return { from, to };
}

export interface TimelineSpan {
  kind: CalendarKind;
  /** Percentages of the window. */
  left: number;
  width: number;
}

export function timelineSpans(items: AvailabilityPerson['items'], from: Date, to: Date): TimelineSpan[] {
  const total = to.getTime() - from.getTime();
  const spans: TimelineSpan[] = [];
  for (const item of items) {
    const start = Math.max(new Date(item.startsAt).getTime(), from.getTime());
    const end = Math.min(new Date(item.endsAt).getTime(), to.getTime());
    if (end <= start) continue;
    spans.push({
      kind: item.kind,
      left: ((start - from.getTime()) / total) * 100,
      width: ((end - start) / total) * 100,
    });
  }
  return spans;
}
