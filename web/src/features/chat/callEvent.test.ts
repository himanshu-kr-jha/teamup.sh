import { describe, expect, it } from 'vitest';
import { callEventView } from './callEvent';

describe('a call that did not happen, in the conversation', () => {
  it('tells the person called they missed it, in red, and the caller that nobody answered', () => {
    expect(callEventView('Missed call', false, 'Ravi')).toEqual({ kind: 'missed', alert: true, text: 'Missed call from Ravi' });
    expect(callEventView('Missed call', true, 'Ravi')).toEqual({ kind: 'missed', alert: false, text: 'No answer' });
  });
  it('says a declined call was declined, to both, and is not a missed one', () => {
    expect(callEventView('Call declined', false, 'Ravi')).toEqual({ kind: 'declined', alert: false, text: 'You declined a call from Ravi' });
    expect(callEventView('Call declined', true, 'Ravi').text).toBe('Call declined');
  });
  it('says the person called was busy', () => {
    expect(callEventView('Missed call, in another call', true, 'Ravi').text).toBe('Busy, in another call or meeting');
    expect(callEventView('Missed call, in another call', false, 'Ravi')).toEqual({
      kind: 'busy',
      alert: true,
      text: 'Missed call from Ravi while you were in another call',
    });
  });
});
