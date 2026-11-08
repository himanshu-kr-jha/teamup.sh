import { describe, expect, it, vi } from 'vitest';
import { AxiosError, AxiosHeaders } from 'axios';
import { endLocalSession, isSignedOut, restoreSession } from './session';

function axiosFailure(status?: number) {
  const config = { headers: new AxiosHeaders() };
  return new AxiosError(
    'failed',
    undefined,
    config,
    undefined,
    status ? { status, statusText: '', data: {}, headers: {}, config } : undefined
  );
}

describe('isSignedOut', () => {
  it('is true only when the server says the session is gone', () => {
    expect(isSignedOut(axiosFailure(401))).toBe(true);
  });

  it('does not treat a network failure, a 5xx or a stray error as being signed out', () => {
    expect(isSignedOut(axiosFailure())).toBe(false);
    expect(isSignedOut(axiosFailure(500))).toBe(false);
    expect(isSignedOut(axiosFailure(502))).toBe(false);
    expect(isSignedOut(new Error('boom'))).toBe(false);
  });
});

describe('endLocalSession', () => {
  const deps = () => ({
    clearStore: vi.fn(),
    clearQueries: vi.fn(),
    clearOfflineCache: vi.fn().mockResolvedValue(undefined),
    goToLogin: vi.fn(),
  });

  it('forgets the person, their cached data, and sends them to sign in', async () => {
    const d = deps();
    await endLocalSession(d);
    expect(d.clearStore).toHaveBeenCalledOnce();
    expect(d.clearQueries).toHaveBeenCalledOnce();
    expect(d.clearOfflineCache).toHaveBeenCalledOnce();
    expect(d.goToLogin).toHaveBeenCalledOnce();
  });

  it('still leaves the app if the offline cache cannot be cleared', async () => {
    const d = deps();
    d.clearOfflineCache.mockRejectedValue(new Error('no caches here'));
    await endLocalSession(d);
    expect(d.goToLogin).toHaveBeenCalledOnce();
  });
});

describe('restoreSession', () => {
  const answering = (result: Partial<Response> | Error) =>
    vi.stubGlobal('fetch', vi.fn(() => (result instanceof Error ? Promise.reject(result) : Promise.resolve(result))));

  it('takes who the cookie says is signed in', async () => {
    answering({ ok: true, json: async () => ({ user: { id: 'u' }, permissions: ['tasks.all'] }) });
    const setUser = vi.fn();
    expect(await restoreSession(setUser)).toBe(true);
    // With what their roles let them do, which the pages offer by.
    expect(setUser).toHaveBeenCalledWith({ id: 'u', permissions: ['tasks.all'] });
    expect(fetch).toHaveBeenCalledWith('/api/auth/me', { credentials: 'include' });
  });

  it('is no session when the server says so, or cannot say', async () => {
    const setUser = vi.fn();
    answering({ ok: false });
    expect(await restoreSession(setUser)).toBe(false);
    answering(new Error('offline'));
    expect(await restoreSession(setUser)).toBe(false);
    expect(setUser).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });
});
