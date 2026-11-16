import { describe, expect, it } from 'vitest';
import { markLabel, marksOn, type DayMark } from './dayMarks';

const holiday: DayMark = { kind: 'holiday', from: '2026-12-25', to: '2026-12-25', name: 'Christmas Day' };
const week: DayMark = { kind: 'leave', from: '2026-12-21', to: '2026-12-31', part: 'full' };
const open: DayMark = { kind: 'leave', from: '2026-12-28', part: 'full' };
const morning: DayMark = { kind: 'leave', from: '2026-12-18', to: '2026-12-18', part: 'morning' };

describe('marksOn', () => {
  it('finds every mark covering a day, holidays first, by the date it names', () => {
    expect(marksOn([week, holiday], new Date(2026, 11, 25))).toEqual([holiday, week]);
    expect(marksOn([week, holiday], new Date(2026, 11, 20))).toEqual([]);
    expect(marksOn([week], new Date(2026, 11, 31, 23, 59))).toEqual([week]);
  });
  it('leave with no end covers every day from its first', () => {
    expect(marksOn([open], new Date(2027, 5, 1))).toEqual([open]);
    expect(marksOn([open], new Date(2026, 11, 27))).toEqual([]);
  });
});

describe('markLabel', () => {
  it('says what the day is', () => {
    expect(markLabel(holiday)).toBe('Holiday: Christmas Day');
    expect(markLabel(week)).toBe('On leave');
    expect(markLabel(morning)).toBe('On leave, morning');
    expect(markLabel(week, 'Ann')).toBe('Ann on leave');
  });
});
