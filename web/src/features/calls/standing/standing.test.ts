import { describe, expect, it } from 'vitest';
import {
  attendanceCounts,
  attendanceRangeProblem,
  defaultAttendanceRange,
  describeSchedule,
  describeWeekdays,
  orderAttendance,
  standingFormProblem,
  type AttendancePerson,
} from './standing';

describe('describeWeekdays', () => {
  it('reads common patterns as a person would', () => {
    expect(describeWeekdays([1, 2, 3, 4, 5])).toBe('Mon to Fri');
    expect(describeWeekdays([1, 2, 3, 4, 5, 6, 7])).toBe('Every day');
    expect(describeWeekdays([1, 3])).toBe('Mon, Wed');
    expect(describeWeekdays([5, 1])).toBe('Mon, Fri');
    expect(describeWeekdays([6, 7])).toBe('Sat, Sun');
    expect(describeWeekdays([])).toBe('No days');
  });

  it('treats any unbroken run as a range', () => {
    expect(describeWeekdays([2, 3, 4])).toBe('Tue to Thu');
    expect(describeWeekdays([1, 2])).toBe('Mon, Tue');
  });
});

describe('describeSchedule', () => {
  it('says when and in which zone, without saying the zone twice', () => {
    expect(
      describeSchedule({ weekdays: [1, 2, 3, 4, 5], timeOfDay: '09:30', timeZone: 'Asia/Kolkata', durationMinutes: 30 }, 'Asia/Kolkata')
    ).toBe('Mon to Fri at 09:30, 30 min');
    expect(
      describeSchedule({ weekdays: [1], timeOfDay: '17:00', timeZone: 'Europe/Paris', durationMinutes: 90 }, 'Asia/Kolkata')
    ).toBe('Mon at 17:00 Europe/Paris, 1 h 30 min');
  });
});

describe('standingFormProblem', () => {
  const good = { name: 'Morning', timeOfDay: '09:30', timeZone: 'UTC', weekdays: [1], durationMinutes: 30 };

  it('accepts a sensible meeting', () => {
    expect(standingFormProblem(good)).toBeNull();
  });

  it('wants a name, a day, a time and a length up to 480 minutes', () => {
    expect(standingFormProblem({ ...good, name: '  ' })).toMatch(/name/i);
    expect(standingFormProblem({ ...good, weekdays: [] })).toMatch(/day/i);
    expect(standingFormProblem({ ...good, timeOfDay: '' })).toMatch(/time/i);
    expect(standingFormProblem({ ...good, durationMinutes: 0 })).toMatch(/minutes/i);
    expect(standingFormProblem({ ...good, durationMinutes: 481 })).toMatch(/480/);
  });
});

describe('attendance', () => {
  const person = (name: string, status: AttendancePerson['status']): AttendancePerson => ({
    userId: name,
    name,
    status,
    firstJoinAt: null,
    lastLeaveAt: null,
    minutesPresent: 0,
    minutesLate: 0,
  });

  it('counts each status', () => {
    expect(attendanceCounts([person('a', 'present'), person('b', 'absent'), person('c', 'absent'), person('d', 'unknown')])).toEqual({
      present: 1,
      absent: 2,
      unknown: 1,
    });
  });

  it('lists who came first, then who did not, alphabetically', () => {
    const ordered = orderAttendance([person('Zed', 'present'), person('Amy', 'absent'), person('Bob', 'present'), person('Cy', 'unknown')]);
    expect(ordered.map((p) => p.name)).toEqual(['Bob', 'Zed', 'Cy', 'Amy']);
  });

  it('defaults to the last week of local days', () => {
    expect(defaultAttendanceRange(new Date(2026, 10, 7, 12))).toEqual({ from: '2026-11-01', to: '2026-11-07' });
  });

  it('refuses a backwards range and one over 92 days', () => {
    expect(attendanceRangeProblem({ from: '2026-11-07', to: '2026-11-01' })).toMatch(/start/i);
    expect(attendanceRangeProblem({ from: '2026-01-01', to: '2026-11-01' })).toMatch(/92/);
    expect(attendanceRangeProblem({ from: '2026-08-01', to: '2026-11-01' })).toBeNull();
  });
});

describe('defaultAttendanceRange', () => {
  it('ends on today where the meeting is, not where you are', () => {
    // 22:00 on the 7th in New York is already the 8th in Kolkata.
    const now = new Date('2026-11-08T03:00:00Z');
    expect(defaultAttendanceRange(now, 'Asia/Kolkata')).toEqual({ from: '2026-11-02', to: '2026-11-08' });
    expect(defaultAttendanceRange(now, 'America/New_York')).toEqual({ from: '2026-11-01', to: '2026-11-07' });
  });

  it('falls back on your own date until it knows the meeting', () => {
    const now = new Date(2026, 10, 8, 12);
    expect(defaultAttendanceRange(now)).toEqual({ from: '2026-11-02', to: '2026-11-08' });
  });
});
