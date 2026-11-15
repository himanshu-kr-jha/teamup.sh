import { describe, expect, it } from 'vitest';
import type { LeaveRecord } from '@/shared/api/client';
import {
  addDays,
  describeChange,
  describeLeave,
  draftFrom,
  leaveDays,
  leaveDraftBody,
  leaveDraftProblem,
  provenance,
  recordedAfterTheFact,
  splitLeave,
} from './leave';

const today = '2026-11-16';

const record = (over: Partial<LeaveRecord>): LeaveRecord => ({
  id: over.startsOn ?? 'x',
  userId: 'ann',
  userName: 'Ann Archer',
  startsOn: '2026-11-18',
  endsOn: '2026-11-20',
  part: 'full',
  note: '',
  addedBy: { id: 'ann', name: 'Ann Archer' },
  addedAt: '2026-11-10T09:00:00Z',
  changedBy: { id: 'ann', name: 'Ann Archer' },
  changedAt: '2026-11-10T09:00:00Z',
  ...over,
});

describe('leave', () => {
  it('moves dates across a month end', () => {
    expect(addDays('2026-11-30', 1)).toBe('2026-12-01');
    expect(addDays('2026-03-01', -1)).toBe('2026-02-28');
  });

  it('says which days a record covers', () => {
    expect(describeLeave(record({}), today)).toBe('Wed, Nov 18 to Fri, Nov 20');
    expect(describeLeave(record({ endsOn: '2026-11-18' }), today)).toBe('Wed, Nov 18');
    expect(describeLeave(record({ endsOn: null }), today)).toBe('From Wed, Nov 18, no return date yet');
    expect(describeLeave(record({ endsOn: '2026-11-18', part: 'afternoon' }), today)).toBe('Wed, Nov 18, afternoon');
    expect(describeLeave(record({ startsOn: '2027-01-04', endsOn: '2027-01-04' }), today)).toBe('Mon, Jan 4, 2027');
  });

  it('counts days, half days as halves', () => {
    expect(leaveDays(record({}))).toBe(3);
    expect(leaveDays(record({ endsOn: '2026-11-18', part: 'morning' }))).toBe(0.5);
    expect(leaveDays(record({ endsOn: null }))).toBeNull();
  });

  it('puts what is to come first, soonest first, and the past after, latest first', () => {
    const { upcoming, past } = splitLeave(
      [
        record({ startsOn: '2026-12-01', endsOn: '2026-12-02' }),
        record({ startsOn: '2026-11-14', endsOn: '2026-11-17' }),
        record({ startsOn: '2026-10-01', endsOn: '2026-10-01' }),
        record({ startsOn: '2026-11-02', endsOn: '2026-11-03' }),
        record({ startsOn: '2026-09-01', endsOn: null }),
      ],
      today
    );
    expect(upcoming.map((l) => l.startsOn)).toEqual(['2026-09-01', '2026-11-14', '2026-12-01']);
    expect(past.map((l) => l.startsOn)).toEqual(['2026-11-02', '2026-10-01']);
  });

  it('checks a draft before it is sent', () => {
    const draft = { half: false, startsOn: '2026-11-18', endsOn: '2026-11-20', part: 'morning' as const, note: '' };
    expect(leaveDraftProblem(draft)).toBeNull();
    expect(leaveDraftProblem({ ...draft, startsOn: '' })).toMatch(/first day/);
    expect(leaveDraftProblem({ ...draft, endsOn: '2026-11-17' })).toMatch(/before/);
    expect(leaveDraftProblem({ ...draft, endsOn: '2027-11-20' })).toMatch(/year/);
    expect(leaveDraftProblem({ ...draft, note: 'x'.repeat(201) })).toMatch(/200/);
    expect(leaveDraftProblem({ ...draft, endsOn: '' })).toBeNull();
  });

  it('sends a half day as one date and an empty last day as no return date', () => {
    expect(leaveDraftBody({ half: true, startsOn: '2026-11-18', endsOn: '2026-11-25', part: 'afternoon', note: ' Dentist ' })).toEqual({
      startsOn: '2026-11-18',
      endsOn: '2026-11-18',
      part: 'afternoon',
      note: 'Dentist',
    });
    expect(leaveDraftBody({ half: false, startsOn: '2026-11-18', endsOn: '', part: 'morning', note: '' })).toEqual({
      startsOn: '2026-11-18',
      endsOn: null,
      part: 'full',
      note: '',
    });
    expect(draftFrom(record({ endsOn: null })).endsOn).toBe('');
    expect(draftFrom(record({ part: 'afternoon', endsOn: '2026-11-18' }))).toMatchObject({ half: true, part: 'afternoon' });
  });

  it('says who added it, and who changed it', () => {
    expect(provenance(record({}))).toBe('Added by Ann Archer on Nov 10, 2026');
    expect(
      provenance(record({ changedBy: { id: 'mia', name: 'Mia Manager' }, changedAt: '2026-11-12T09:00:00Z' }))
    ).toBe('Added by Ann Archer on Nov 10, 2026. Changed by Mia Manager on Nov 12, 2026');
    expect(provenance(record({ addedBy: null }))).toMatch(/since left/);
  });

  it('notices leave recorded once its first day was already under way', () => {
    expect(recordedAfterTheFact(record({}))).toBe(false);
    expect(recordedAfterTheFact(record({ startsOn: '2026-11-05', addedAt: '2026-11-10T09:00:00Z' }))).toBe(true);
  });

  it('reads a change as a line of history', () => {
    expect(
      describeChange(
        { action: 'changed', startsOn: '2026-11-18', endsOn: '2026-11-19', part: 'full', note: '', actor: { id: 'm', name: 'Mia Manager' }, at: '' },
        today
      )
    ).toBe('Changed by Mia Manager: Wed, Nov 18 to Thu, Nov 19');
  });
});
