import { describe, expect, it } from 'vitest';
import { loginPathFor, safeNext } from './nextPath';

describe('loginPathFor', () => {
  it('remembers where somebody was going', () => {
    expect(loginPathFor('/calls/abc', '')).toBe('/login?next=%2Fcalls%2Fabc');
    expect(loginPathFor('/calendar', '?date=2026-11-07')).toBe('/login?next=%2Fcalendar%3Fdate%3D2026-11-07');
  });

  it('has nothing to remember for the front door', () => {
    expect(loginPathFor('/', '')).toBe('/login');
    expect(loginPathFor('/chat', '')).toBe('/login');
    expect(loginPathFor('/login', '')).toBe('/login');
  });
});

describe('safeNext', () => {
  it('goes to a path inside the app', () => {
    expect(safeNext('/calls/abc')).toBe('/calls/abc');
    expect(safeNext('/calendar?date=2026-11-07')).toBe('/calendar?date=2026-11-07');
  });

  it('falls back when there is nothing', () => {
    expect(safeNext(null)).toBe('/chat');
    expect(safeNext('')).toBe('/chat');
  });

  it('never sends somebody off the site', () => {
    for (const bad of ['https://evil.example', '//evil.example', '/\\evil.example', 'evil', 'javascript:alert(1)', '/\t/evil.example']) {
      expect(safeNext(bad)).toBe('/chat');
    }
  });

  it('does not go back to the login page', () => {
    expect(safeNext('/login')).toBe('/chat');
    expect(safeNext('/login?next=/x')).toBe('/chat');
  });
});
