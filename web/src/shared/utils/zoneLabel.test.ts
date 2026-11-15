import { describe, expect, it } from 'vitest';
import { zoneLabel } from './zoneLabel';

describe('zoneLabel', () => {
  const july = new Date('2026-07-01T12:00:00Z');
  it('names India the same under either spelling, with its offset', () => {
    expect(zoneLabel('Asia/Calcutta', july)).toBe('India Standard Time (GMT+5:30)');
    expect(zoneLabel('Asia/Kolkata', july)).toBe('India Standard Time (GMT+5:30)');
  });
  it('follows daylight saving', () => {
    expect(zoneLabel('Europe/London', july)).toBe('British Summer Time (GMT+1)');
  });
  it('does not repeat an offset that is already the name', () => {
    expect(zoneLabel('Etc/GMT-3', july)).toBe('GMT+03:00');
  });
  it('falls back to the identifier for something it cannot read', () => {
    expect(zoneLabel('Not/A_Zone', july)).toBe('Not/A Zone');
  });
});
