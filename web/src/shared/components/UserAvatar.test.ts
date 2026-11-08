import { describe, expect, it } from 'vitest';
import { initialsOf } from './UserAvatar';
import { centreSquare } from '../squareImage';

describe('initials', () => {
  it('takes the first and last word', () => {
    expect(initialsOf('Ada Lovelace')).toBe('AL');
    expect(initialsOf('Mary Ann Evans')).toBe('ME');
    expect(initialsOf('ada')).toBe('A');
    expect(initialsOf('  ')).toBe('?');
    expect(initialsOf(undefined)).toBe('?');
  });
});

describe('a profile picture is the middle square', () => {
  it('of a landscape photo', () => {
    expect(centreSquare(400, 200)).toEqual({ sx: 100, sy: 0, side: 200 });
  });
  it('of a portrait photo', () => {
    expect(centreSquare(300, 500)).toEqual({ sx: 0, sy: 100, side: 300 });
  });
});
