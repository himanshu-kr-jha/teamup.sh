import { describe, expect, it } from 'vitest';
import { backOnText, leaveSentence, onLeave } from './leaveNotice';

const today = new Date(2026, 10, 16, 10, 0);

describe('backOnText', () => {
  it('says when somebody is back, from the first day back', () => {
    expect(backOnText('2026-11-17T00:00:00Z', today)).toBe('back tomorrow');
    expect(backOnText('2026-11-19T00:00:00Z', today)).toBe('back on Thursday, 19 November');
    expect(backOnText('2027-01-04T00:00:00Z', today)).toBe('back on Monday, 4 January 2027');
    expect(backOnText(undefined, today)).toBe('with no return date yet');
  });
});

describe('leaveSentence', () => {
  it('names them by their first name', () => {
    expect(leaveSentence({ userId: 'a', name: 'Ann Idle', status: 'on_leave', until: '2026-11-19T00:00:00Z' }, today)).toBe(
      'Ann is on leave, back on Thursday, 19 November.'
    );
  });
  it('only finds somebody who is on leave', () => {
    expect(onLeave({ userId: 'a', name: 'Ann', status: 'available' })).toBeUndefined();
    expect(onLeave(undefined)).toBeUndefined();
  });
});
