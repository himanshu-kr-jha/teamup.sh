import { describe, expect, it } from 'vitest';
import { describeAccess } from './ProjectAccess';

const at = '2026-11-10T10:00:00Z';

describe('describeAccess', () => {
  it('says who did what to whom', () => {
    expect(describeAccess({ id: '1', subject: 'Alice', actor: 'Alice', action: 'requested', detail: 'write: fixing sign-in', at })).toBe('Alice asked for write: fixing sign-in');
    expect(describeAccess({ id: '2', subject: 'Alice', actor: 'Olivia', action: 'approved', detail: 'write', at })).toBe("Alice's request for write access was approved by Olivia");
    expect(describeAccess({ id: '3', subject: 'Alice', actor: 'Olivia', action: 'granted', detail: 'write access on 2 of 2 repositories', at })).toBe('Alice was given write access on 2 of 2 repositories by Olivia');
    expect(describeAccess({ id: '4', subject: 'Alice', actor: 'Olivia', action: 'revoked', detail: 'on 2 of 2 repositories', at })).toBe("Alice's access was removed on 2 of 2 repositories by Olivia");
    expect(describeAccess({ id: '5', subject: '@stranger', action: 'outside_added', detail: 'acme/web on GitHub by @boss, not through TeamUp', at })).toBe('@stranger was added to acme/web on GitHub by @boss, not through TeamUp');
    expect(describeAccess({ id: '6', subject: 'Alice', action: 'found', detail: 'write access to 2 of 2 repositories', at })).toBe('Alice already had write access to 2 of 2 repositories on GitHub or Bitbucket');
    expect(describeAccess({ id: '7', subject: 'Alice', action: 'lost', at })).toBe('Alice no longer has access on GitHub or Bitbucket');
  });
});
