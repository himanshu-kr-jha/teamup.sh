import { describe, expect, it } from 'vitest';
import { possessive, whoseCalendar, type CalendarGroup } from './people';

const groups: CalendarGroup[] = [
  { teamId: 't1', name: 'Backend', people: [{ id: 'alice', name: 'Alice Archer' }] },
  { teamId: 't2', name: 'Platform', people: [{ id: 'pat', name: 'Pat Platform' }] },
];

describe('whoseCalendar', () => {
  it('is your own with no person in the address, or yourself', () => {
    expect(whoseCalendar(null, 'me', groups)).toEqual({ id: 'me', own: true, name: null });
    expect(whoseCalendar('', 'me', groups)).toEqual({ id: 'me', own: true, name: null });
    expect(whoseCalendar('me', 'me', groups)).toEqual({ id: 'me', own: true, name: null });
  });

  it('is somebody else, named from the picker, in any of its teams', () => {
    expect(whoseCalendar('pat', 'me', groups)).toEqual({ id: 'pat', own: false, name: 'Pat Platform' });
  });

  it('names somebody the picker does not list from the member list, or not at all', () => {
    const members = [{ id: 'sam', name: 'Sam Sales' }];
    expect(whoseCalendar('sam', 'me', groups, members)).toEqual({ id: 'sam', own: false, name: 'Sam Sales' });
    expect(whoseCalendar('nobody', 'me', groups, members)).toEqual({ id: 'nobody', own: false, name: null });
  });

  it('waits for the session before it decides', () => {
    expect(whoseCalendar(null, undefined, [])).toEqual({ id: '', own: true, name: null });
  });
});

describe('possessive', () => {
  it('reads as English', () => {
    expect(possessive('Alice Archer')).toBe("Alice Archer's");
    expect(possessive('James')).toBe("James'");
    expect(possessive(null)).toBe('Their');
  });
});
