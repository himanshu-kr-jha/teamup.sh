import { describe, expect, it } from 'vitest';
import { incomingWording, namesList, outcomeMessage, withRinging, withoutRinging } from './addPeople';

describe('ringing people into a call', () => {
  it('names who else is there, briefly', () => {
    expect(namesList([])).toBe('');
    expect(namesList(['Ravi'])).toBe('Ravi');
    expect(namesList(['Ravi', 'Sam'])).toBe('Ravi and Sam');
    expect(namesList(['Ravi', 'Sam', 'Tom'])).toBe('Ravi, Sam and Tom');
    expect(namesList(['Ravi', 'Sam', 'Tom', 'Uma'])).toBe('Ravi, Sam and 2 others');
  });

  it('says a ring is into a call already going, and with whom', () => {
    expect(incomingWording('Asha', { ongoing: true, with: ['Ravi'] })).toBe('Asha is calling you into a call with Ravi.');
    expect(incomingWording('Asha', { ongoing: true })).toBe('Asha is calling you into a call.');
    expect(incomingWording('Asha')).toBe('Asha is calling.');
    expect(incomingWording('Daily stand-up', { standing: true })).toBe('Daily stand-up is starting now.');
  });

  it('tells the call how a ring ended, and keeps quiet when there is nothing to tell', () => {
    expect(outcomeMessage('Ravi', 'declined')).toBe('Ravi declined.');
    expect(outcomeMessage('Ravi', 'no_answer')).toBe('Ravi did not answer.');
    expect(outcomeMessage('Ravi', 'busy')).toMatch(/another call/);
    expect(outcomeMessage('Ravi', 'on_leave')).toMatch(/on leave/);
    expect(outcomeMessage('Ravi', 'answered')).toBeNull();
    expect(outcomeMessage('Ravi', 'cancelled')).toBeNull();
  });

  it('keeps each person being rung once, and lets them go', () => {
    const ravi = { userId: 'r', name: 'Ravi' };
    const sam = { userId: 's', name: 'Sam' };
    let list = withRinging([], ravi);
    list = withRinging(list, ravi);
    list = withRinging(list, sam);
    expect(list).toEqual([ravi, sam]);
    expect(withoutRinging(list, ['r'])).toEqual([sam]);
    // Nothing to take away is the same list, so nothing renders again.
    expect(withoutRinging(list, ['x'])).toBe(list);
  });
});
