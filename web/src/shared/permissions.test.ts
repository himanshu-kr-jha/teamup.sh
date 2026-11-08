import { describe, expect, it } from 'vitest';
import { PERMS, can, grants } from './permissions';

describe('permissions', () => {
  it('go by what the roles hold, not their names', () => {
    expect(can({ roles: ['Office manager'], permissions: ['org.settings'] }, PERMS.orgSettings)).toBe(true);
    expect(can({ roles: ['admin'], permissions: [] }, PERMS.orgSettings)).toBe(false);
    expect(can({ roles: ['owner'], permissions: ['*'] }, PERMS.notesReadAll)).toBe(true);
    expect(can(null, PERMS.orgSettings)).toBe(false);
  });

  it('fall back to the built-in roles for a user remembered from before', () => {
    expect(can({ roles: ['admin'] }, PERMS.members)).toBe(true);
    expect(can({ roles: ['member'] }, PERMS.members)).toBe(false);
  });

  it('let nobody grant what they lack', () => {
    expect(grants(['tasks.all'], 'tasks.all')).toBe(true);
    expect(grants(['tasks.all'], 'org.settings')).toBe(false);
    expect(grants(['*'], 'org.settings')).toBe(true);
    expect(grants(undefined, 'tasks.all')).toBe(false);
  });
});
