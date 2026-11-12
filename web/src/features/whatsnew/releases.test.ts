import { describe, expect, it } from 'vitest';
import { LATEST, RELEASES, unseen } from './releases';

describe("what's new", () => {
  it('lists updates newest first, each with its own id and something in it', () => {
    const ids = RELEASES.map((r) => r.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect([...ids].sort().reverse()).toEqual(ids);
    expect(LATEST).toBe(ids[0]);
    for (const r of RELEASES) {
      expect(r.id).toMatch(/^\d{4}-\d{2}-\d{2}(-[a-z0-9]{1,10})?$/);
      expect(r.changes.length).toBeGreaterThan(0);
    }
  });

  it('shows what came since somebody last looked, and only the newest to somebody who never has', () => {
    expect(unseen(LATEST)).toEqual([]);
    expect(unseen(null).map((r) => r.id)).toEqual([LATEST]);
    const older = RELEASES[RELEASES.length - 1].id;
    expect(unseen(older).map((r) => r.id)).toEqual(RELEASES.slice(0, -1).map((r) => r.id));
  });
});
