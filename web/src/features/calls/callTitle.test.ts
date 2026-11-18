import { describe, expect, it } from 'vitest';
import { callTitleFor, firstNameOf } from './callTitle';

const two = [
  { userId: 'olivia', userName: 'Olivia Owner' },
  { userId: 'alice', userName: 'Alice Archer' },
];

describe('callTitleFor', () => {
  it('shows the person called the caller, not themselves', () => {
    expect(callTitleFor('Alice Archer', two, 'alice')).toBe('Olivia Owner');
    expect(callTitleFor('Alice Archer', two, 'olivia')).toBe('Alice Archer');
  });

  it('leaves a meeting or a channel call its own name', () => {
    expect(callTitleFor('Release planning', two, 'alice')).toBe('Release planning');
    expect(callTitleFor('Alice Archer', [...two, { userId: 'sam', userName: 'Sam Sales' }], 'alice')).toBe('Alice Archer');
  });

  it('is nothing for a call with no name', () => {
    expect(callTitleFor('  ', two, 'alice')).toBeUndefined();
    expect(callTitleFor(undefined, two, 'alice')).toBeUndefined();
  });
});

describe('firstNameOf', () => {
  it('is the first word', () => {
    expect(firstNameOf('Alice Archer')).toBe('Alice');
    expect(firstNameOf('Zed')).toBe('Zed');
  });
});
