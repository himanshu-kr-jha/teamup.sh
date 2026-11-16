import { describe, expect, it } from 'vitest';
import { coordinates, stampLines } from './FieldWork';
import { describeWindow } from './standing';

describe('describeWindow', () => {
  it('says when joining counts, on the meeting clock', () => {
    expect(describeWindow('09:00', 30, 30)).toBe('08:30 to 09:30');
    expect(describeWindow('18:00', 15, 45)).toBe('17:45 to 18:45');
    expect(describeWindow('00:10', 30, 30)).toBe('23:40 to 00:40');
  });
});

describe('the stamp on a field work photo', () => {
  it('writes coordinates with their hemispheres', () => {
    expect(coordinates(23.3441, 85.3096)).toBe('23.34410° N, 85.30960° E');
    expect(coordinates(-33.8688, -151.2093)).toBe('33.86880° S, 151.20930° W');
  });
  it('has when, where and how closely, and the note, cut short when long', () => {
    const lines = stampLines(new Date(2026, 10, 17, 10, 24), { latitude: 23.3441, longitude: 85.3096, accuracyM: 12.4 }, 'Survey at block C');
    expect(lines).toHaveLength(3);
    expect(lines[0]).toMatch(/17 Nov 2026/);
    expect(lines[1]).toBe('23.34410° N, 85.30960° E  ±12 m');
    expect(lines[2]).toBe('Survey at block C');
    expect(stampLines(new Date(), { latitude: 1, longitude: 1, accuracyM: 5 }, ' ')).toHaveLength(2);
    expect(stampLines(new Date(), { latitude: 1, longitude: 1, accuracyM: 5 }, 'x'.repeat(100))[2]).toHaveLength(70);
  });
});
