import { describe, expect, it } from 'vitest';
import type { AccessGrant, ProjectAccess, ProjectRepo, RepoAccess } from '@/shared/api/client';
import { branchStanding } from './StartDevelopment';

const web: ProjectRepo = { id: 'r1', provider: 'github', fullName: 'acme/photon-web', url: '' };
const grant = (level: 'read' | 'write', status: RepoAccess['status']): AccessGrant => ({
  id: 'g',
  projectId: 'p',
  userId: 'me',
  userName: 'Me',
  level,
  source: 'teamup',
  grantedAt: '',
  repositories: [{ repository: 'acme/photon-web', provider: 'github', status }],
});
const access = (over: Partial<ProjectAccess> = {}): ProjectAccess => ({ grants: [], requests: [], unlinked: [], ...over });

describe('who may start a branch', () => {
  it('is somebody with write access that reached the repository', () => {
    expect(branchStanding(access({ grants: [grant('write', 'granted')] }), 'me', web)).toBe('ok');
    expect(branchStanding(access({ grants: [grant('write', 'existing')] }), 'me', web)).toBe('ok');
  });

  it('is not somebody with read access, or an invitation still to accept', () => {
    expect(branchStanding(access({ grants: [grant('read', 'granted')] }), 'me', web)).toBe('none');
    expect(branchStanding(access({ grants: [grant('write', 'invited')] }), 'me', web)).toBe('invited');
  });

  it('says what to do next: link an account, or wait for the request', () => {
    expect(branchStanding(access({ unlinked: ['github'] }), 'me', web)).toBe('unlinked');
    const pending = { id: 'q', projectId: 'p', projectName: 'Photon', userId: 'me', userName: 'Me', level: 'write' as const, reason: '', status: 'pending' as const, createdAt: '' };
    expect(branchStanding(access({ requests: [pending] }), 'me', web)).toBe('requested');
  });

  it('reads only the caller among the grants an administrator sees', () => {
    const theirs = { ...grant('write', 'granted'), userId: 'someone else' };
    expect(branchStanding(access({ grants: [theirs] }), 'me', web)).toBe('none');
  });
});
