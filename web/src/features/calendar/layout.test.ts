import { describe, expect, it, vi } from 'vitest';
import {
  availabilityAt,
  blockProblem,
  conflictsAt,
  dayLabelKey,
  defaultSlot,
  fromInputValue,
  hasOverlap,
  itemKey,
  itemLabel,
  joinTarget,
  meetingStillOn,
  layoutDay,
  meetingProblem,
  minuteToDate,
  nowOffset,
  rangeForDays,
  segmentsForDay,
  shiftDays,
  slotFromOffset,
  timelineSpans,
  timelineWindow,
  toInputValue,
  visibleDays,
  type CalendarItem,
} from './layout';

// Runs forked (see vitest.config.ts) so that this takes effect: this zone changes
// its clocks on 2026-11-01, which is what breaks naive day arithmetic.
vi.stubEnv('TZ', 'America/New_York');

const local = (y: number, m: number, d: number, h = 0, min = 0) => new Date(y, m - 1, d, h, min);
const item = (start: Date, end: Date, kind: CalendarItem['kind'] = 'busy', extra: Partial<CalendarItem> = {}): CalendarItem => ({
  kind,
  startsAt: start.toISOString(),
  endsAt: end.toISOString(),
  ...extra,
});
const day = local(2026, 11, 10);
const at = (h: number, m = 0) => local(2026, 11, 10, h, m);

describe('visibleDays', () => {
  it('shows Monday to Sunday around the anchor on a computer', () => {
    const days = visibleDays(local(2026, 11, 11), 'week');
    expect(days).toHaveLength(7);
    expect(days[0].getDay()).toBe(1);
    expect(days[0].getDate()).toBe(9);
    expect(days[6].getDate()).toBe(15);
  });

  it('shows one day on a phone', () => {
    const days = visibleDays(local(2026, 11, 11, 15), 'day');
    expect(days).toHaveLength(1);
    expect(days[0].getHours()).toBe(0);
  });

  it('walks across a daylight saving change without losing a day', () => {
    const days = visibleDays(local(2026, 10, 29), 'week');
    expect(days.map((d) => d.getDate())).toEqual([26, 27, 28, 29, 30, 31, 1]);
    const after = visibleDays(local(2026, 11, 1), 'week');
    expect(after[6].getHours()).toBe(0);
  });
});

describe('rangeForDays', () => {
  it('covers whole local days', () => {
    const { from, to } = rangeForDays(visibleDays(local(2026, 11, 11), 'day'));
    expect(new Date(from).getTime()).toBe(local(2026, 11, 11).getTime());
    expect(new Date(to).getTime()).toBe(local(2026, 11, 12).getTime());
  });
});

describe('shiftDays', () => {
  it('moves by the view', () => {
    expect(shiftDays(local(2026, 11, 11), 'week', 1).getDate()).toBe(18);
    expect(shiftDays(local(2026, 11, 11), 'day', -1).getDate()).toBe(10);
  });
});

describe('segmentsForDay', () => {
  it('places an event by its minutes', () => {
    const [segment] = segmentsForDay([item(at(9, 30), at(10, 15))], day);
    expect(segment.startMinute).toBe(570);
    expect(segment.endMinute).toBe(615);
    expect(segment.startsBeforeDay).toBe(false);
  });

  it('leaves out what is not on this day', () => {
    expect(segmentsForDay([item(local(2026, 11, 9, 9), local(2026, 11, 9, 10))], day)).toEqual([]);
    expect(segmentsForDay([item(local(2026, 11, 11, 0), local(2026, 11, 11, 1))], day)).toEqual([]);
    // Ending exactly at midnight is not on the next day.
    expect(segmentsForDay([item(local(2026, 11, 9, 23), day)], day)).toEqual([]);
  });

  it('cuts something crossing midnight into a piece on each day', () => {
    const crossing = item(local(2026, 11, 10, 22), local(2026, 11, 11, 2));
    const [first] = segmentsForDay([crossing], day);
    const [second] = segmentsForDay([crossing], local(2026, 11, 11));
    expect(first).toMatchObject({ startMinute: 1320, endMinute: 1440, endsAfterDay: true, startsBeforeDay: false });
    expect(second).toMatchObject({ startMinute: 0, endMinute: 120, startsBeforeDay: true, endsAfterDay: false });
  });

  it('fills every day of something spanning several', () => {
    const long = item(local(2026, 11, 9, 12), local(2026, 11, 12, 12), 'focus');
    expect(segmentsForDay([long], day)[0]).toMatchObject({ startMinute: 0, endMinute: 1440 });
  });

  it('keeps a wall clock position on the day the clocks change', () => {
    const change = local(2026, 11, 1);
    const [segment] = segmentsForDay([item(local(2026, 11, 1, 9), local(2026, 11, 1, 10))], change);
    expect(segment.startMinute).toBe(540);
    expect(segment.endMinute).toBe(600);
  });

  it('never gives a zero height event', () => {
    const [segment] = segmentsForDay([item(at(9), at(9))], day);
    expect(segment.endMinute).toBeGreaterThan(segment.startMinute);
  });
});

describe('layoutDay', () => {
  const place = (items: CalendarItem[], maxColumns = 3) => layoutDay(segmentsForDay(items, day), { maxColumns });

  it('gives a lone event the full width', () => {
    const { events } = place([item(at(9), at(10))]);
    expect(events[0]).toMatchObject({ column: 0, columns: 1 });
  });

  it('puts two overlapping events side by side', () => {
    const { events } = place([item(at(9), at(10)), item(at(9, 30), at(10, 30))]);
    expect(events.map((e) => e.columns)).toEqual([2, 2]);
    expect(events.map((e) => e.column).sort()).toEqual([0, 1]);
  });

  it('does not split back-to-back events', () => {
    const { events } = place([item(at(9), at(10)), item(at(10), at(11))]);
    expect(events.every((e) => e.columns === 1)).toBe(true);
  });

  it('reuses a column once it is free', () => {
    const { events } = place([item(at(9), at(10)), item(at(9), at(11)), item(at(10), at(11))]);
    expect(events.every((e) => e.columns === 2)).toBe(true);
  });

  it('treats a very short event as tall enough to read, so a neighbour does not cover it', () => {
    const { events } = place([item(at(9), at(9, 5)), item(at(9, 10), at(9, 15))]);
    expect(events.map((e) => e.columns)).toEqual([2, 2]);
  });

  it('collapses what does not fit into a chip that says how many', () => {
    const { events, overflow } = place([
      item(at(9), at(10)),
      item(at(9), at(10)),
      item(at(9), at(10)),
      item(at(9), at(10)),
      item(at(9), at(10)),
    ]);
    expect(events).toHaveLength(2);
    expect(events.every((e) => e.columns === 3)).toBe(true);
    expect(overflow).toHaveLength(1);
    expect(overflow[0]).toMatchObject({ column: 2, columns: 3, startMinute: 540, endMinute: 600 });
    expect(overflow[0].segments).toHaveLength(3);
  });

  it('draws open slots behind events and leaves a strip of them showing', () => {
    const { events, background } = place([item(at(9), at(12), 'open'), item(at(10), at(11)), item(at(13), at(14))]);
    expect(background).toHaveLength(1);
    expect(events).toHaveLength(2);
    expect(events.every((e) => e.columns === 1)).toBe(true);
    expect(events[0].inset).toBe(true);
    expect(events[1].inset).toBe(false);
  });
});

describe('hasOverlap', () => {
  it('is false for touching ends', () => {
    expect(hasOverlap(at(9), at(10), at(10), at(11))).toBe(false);
    expect(hasOverlap(at(9), at(10, 1), at(10), at(11))).toBe(true);
  });
});

describe('itemLabel', () => {
  it('shows a title only when the server sent one', () => {
    expect(itemLabel(item(at(9), at(10), 'busy', { title: 'Planning' }))).toBe('Planning');
    expect(itemLabel(item(at(9), at(10), 'busy'))).toBe('Busy');
    expect(itemLabel(item(at(9), at(10), 'focus'))).toBe('Focus time');
    expect(itemLabel(item(at(9), at(10), 'open'))).toBe('Open for meetings');
    expect(itemLabel(item(at(9), at(10), 'busy', { title: '  ' }))).toBe('Busy');
  });

  it('names an untitled call by what it is, when the server says what it is', () => {
    expect(itemLabel(item(at(9), at(10), 'busy', { source: 'call' }))).toBe('Call');
  });
});

describe('itemKey', () => {
  it('uses the id when there is one and stays unique when there is not', () => {
    expect(itemKey(item(at(9), at(10), 'busy', { id: 'x' }), 3)).toBe('x');
    const anon = item(at(9), at(10));
    expect(itemKey(anon, 0)).not.toBe(itemKey(anon, 1));
  });
});

describe('nowOffset', () => {
  it('is the minutes today and nothing on other days', () => {
    expect(nowOffset(at(9, 30), day)).toBe(570);
    expect(nowOffset(local(2026, 11, 11, 9), day)).toBeNull();
  });
});

describe('a meeting whose room emptied before its end', () => {
  const ended = (endsAt: Date) => ({ status: 'ended', scheduledEnd: endsAt.toISOString() });

  it('is still on until then, and not after', () => {
    expect(meetingStillOn(ended(at(11)), at(10, 59))).toBe(true);
    expect(meetingStillOn(ended(at(11)), at(11))).toBe(false);
  });

  it('is not about a meeting that is not over, a standing room, or an ordinary call', () => {
    expect(meetingStillOn({ status: 'active', scheduledEnd: at(11).toISOString() }, at(10))).toBe(false);
    expect(meetingStillOn({ ...ended(at(11)), standingMeetingId: 's' }, at(10))).toBe(false);
    expect(meetingStillOn({ status: 'ended' }, at(10))).toBe(false);
  });
});

describe('joinTarget', () => {
  const meeting = (extra: Partial<CalendarItem> = {}) =>
    item(at(10), at(11), 'busy', { source: 'meeting', callId: 'c', status: 'scheduled', ...extra });

  it('offers a meeting from 30 minutes before until it ends', () => {
    expect(joinTarget(meeting(), at(9, 29))).toBeNull();
    expect(joinTarget(meeting(), at(9, 30))).toEqual({ type: 'call', callId: 'c' });
    expect(joinTarget(meeting(), at(10, 59))).toEqual({ type: 'call', callId: 'c' });
    expect(joinTarget(meeting(), at(11))).toBeNull();
  });

  it('offers a meeting that has already been opened', () => {
    expect(joinTarget(meeting({ status: 'active' }), at(10))).toEqual({ type: 'call', callId: 'c' });
  });

  it('does not offer a cancelled or ended one, or a block', () => {
    expect(joinTarget(meeting({ status: 'cancelled' }), at(10))).toBeNull();
    // The room emptied early, and it is still on until its end, from when it opens.
    expect(joinTarget(meeting({ status: 'ended' }), at(9, 29))).toBeNull();
    expect(joinTarget(meeting({ status: 'ended' }), at(9, 30))).toEqual({ type: 'call', callId: 'c' });
    expect(joinTarget(meeting({ status: 'ended' }), at(10, 59))).toEqual({ type: 'call', callId: 'c' });
    expect(joinTarget(meeting({ status: 'ended' }), at(11))).toBeNull();
    expect(joinTarget(item(at(10), at(11), 'focus', { source: 'block', id: 'b' }), at(10))).toBeNull();
  });

  it('offers a live ordinary call and not a finished one', () => {
    const call = { source: 'call', callId: 'c' } as const;
    expect(joinTarget(item(at(10), at(11), 'busy', { ...call, status: 'active' }), at(10, 30))).toEqual({ type: 'call', callId: 'c' });
    expect(joinTarget(item(at(10), at(11), 'busy', { ...call, status: 'ended' }), at(10, 30))).toBeNull();
  });

  it('offers a standing meeting for the rest of its day', () => {
    const standing = item(at(10), at(10, 30), 'busy', { source: 'standing', standingMeetingId: 's' });
    expect(joinTarget(standing, at(9))).toBeNull();
    expect(joinTarget(standing, at(9, 45))).toEqual({ type: 'standing', standingMeetingId: 's' });
    expect(joinTarget(standing, at(20))).toEqual({ type: 'standing', standingMeetingId: 's' });
    expect(joinTarget(standing, local(2026, 11, 11, 0, 1))).toBeNull();
  });

  it('offers nothing for an item the server gave no detail on', () => {
    expect(joinTarget(item(at(10), at(11)), at(10, 30))).toBeNull();
  });
});

describe('availability', () => {
  const people = [
    { userId: 'free', items: [item(at(7), at(8))] },
    { userId: 'busy', items: [item(at(9, 30), at(10, 30))] },
    { userId: 'focus', items: [item(at(9), at(10), 'focus')] },
    { userId: 'open', items: [item(at(8), at(12), 'open')] },
    { userId: 'both', items: [item(at(9), at(10), 'open'), item(at(9), at(9, 30), 'focus'), item(at(9), at(9, 30))] },
  ];

  it('says the most limiting thing for the time asked', () => {
    const answers = Object.fromEntries(people.map((p) => [p.userId, availabilityAt(p.items, at(9), at(10))]));
    expect(answers).toEqual({ free: 'free', busy: 'busy', focus: 'focus', open: 'open', both: 'busy' });
  });

  it('counts only busy and focus as conflicts', () => {
    expect(conflictsAt(people, at(9), at(10))).toEqual([
      { userId: 'busy', state: 'busy' },
      { userId: 'focus', state: 'focus' },
      { userId: 'both', state: 'busy' },
    ]);
  });

  it('does not count touching ends', () => {
    expect(conflictsAt(people, at(10, 30), at(11))).toEqual([]);
  });

  it('can overlook the meeting being edited, which would otherwise conflict with itself', () => {
    const editing = { startsAt: at(9, 30).toISOString(), endsAt: at(10, 30).toISOString() };
    expect(conflictsAt([people[1]], at(9), at(10), editing)).toEqual([]);
  });
});

describe('input values', () => {
  it('round trips the local time', () => {
    const date = local(2026, 11, 10, 9, 5);
    expect(toInputValue(date)).toBe('2026-11-10T09:05');
    expect(fromInputValue('2026-11-10T09:05')?.getTime()).toBe(date.getTime());
    expect(fromInputValue('')).toBeNull();
    expect(fromInputValue('nope')).toBeNull();
  });
});

describe('form problems', () => {
  it('checks a block against the server limits', () => {
    expect(blockProblem(at(10), at(9))).toMatch(/after/i);
    expect(blockProblem(at(10), at(10))).toMatch(/after/i);
    expect(blockProblem(null, at(10))).toMatch(/start/i);
    expect(blockProblem(local(2026, 11, 1), local(2026, 12, 1))).toMatch(/30 days/);
    expect(blockProblem(at(9), at(10))).toBeNull();
  });

  it('checks a meeting against the server limits', () => {
    expect(meetingProblem('', at(9), at(10), 1)).toMatch(/name/i);
    expect(meetingProblem('Plan', at(10), at(9), 1)).toMatch(/after/i);
    expect(meetingProblem('Plan', at(9), local(2026, 11, 11, 10), 1)).toMatch(/24 hours/);
    expect(meetingProblem('Plan', at(9), at(10), 201)).toMatch(/200/);
    expect(meetingProblem('Plan', at(9), at(10), 0)).toBeNull();
  });
});

describe('defaultSlot', () => {
  it('starts at the next half hour and lasts half an hour', () => {
    const { start, end } = defaultSlot(at(9, 10));
    expect(start.getTime()).toBe(at(9, 30).getTime());
    expect(end.getTime()).toBe(at(10).getTime());
  });

  it('crosses midnight properly', () => {
    const { start } = defaultSlot(at(23, 50));
    expect(start.getTime()).toBe(local(2026, 11, 11).getTime());
  });
});

describe('slotFromOffset', () => {
  it('rounds a click down to the half hour and lasts half an hour', () => {
    const { start, end } = slotFromOffset(day, 9.6 * 60);
    expect(start.getTime()).toBe(at(9, 30).getTime());
    expect(end.getTime()).toBe(at(10).getTime());
  });
});

describe('minuteToDate', () => {
  it('finds the wall clock time on the day the clocks change', () => {
    const date = minuteToDate(local(2026, 11, 1), 9 * 60);
    expect(date.getHours()).toBe(9);
  });
});

describe('dayLabelKey', () => {
  it('is a stable local day', () => {
    expect(dayLabelKey(at(9))).toBe('2026-11-10');
  });
});

describe('timelineWindow', () => {
  it('shows working hours for something inside them', () => {
    const { from, to } = timelineWindow(at(10), at(11));
    expect(from.getHours()).toBe(7);
    expect(to.getHours()).toBe(20);
  });

  it('widens to take in something outside them', () => {
    const { from, to } = timelineWindow(at(6), at(21));
    expect(from.getHours()).toBe(5);
    expect(to.getHours()).toBe(22);
  });
});

describe('timelineSpans', () => {
  it('places each item as a share of the window, cut to fit', () => {
    const spans = timelineSpans(
      [item(at(7), at(8)), item(at(10), at(11), 'focus'), item(at(15), at(23), 'open'), item(at(1), at(2))],
      at(8),
      at(16)
    );
    expect(spans).toEqual([
      { kind: 'focus', left: 25, width: 12.5 },
      { kind: 'open', left: 87.5, width: 12.5 },
    ]);
  });
});
