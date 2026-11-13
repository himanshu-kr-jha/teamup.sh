// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { onlineManager } from '@tanstack/react-query';
import { AxiosError, AxiosHeaders } from 'axios';
import { endLocalSession, isSignedOut, restoreSession, signOutLocally } from './session';
import { useAuthStore } from '@/shared/store/authStore';
import { queryClient } from '@/shared/api/queryClient';
import { fakeCaches } from '@/test/fakeCaches';

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

describe('signOutLocally', () => {
  const someone = { id: 'u1', name: 'Ann', email: 'ann@example.com', roles: [], orgId: 'o' };

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    onlineManager.setOnline(true);
    localStorage.clear();
    window.history.replaceState(null, '', '/');
  });

  function signedIn() {
    useAuthStore.getState().setUser(someone);
    queryClient.setQueryData(['messages', 'c1'], [{ id: 'm1', text: 'secret' }]);
    localStorage.setItem('teamup.call.background.custom', 'data:image/jpeg;base64,me');
    localStorage.setItem('teamup.upload.chat:c1:a.mp4:1:2', 'up1');
    localStorage.setItem('theme', 'dark');
    window.history.replaceState(null, '', '/chat/c1');
    return vi.spyOn(window.location, 'assign').mockImplementation(() => undefined);
  }

  it('leaves nothing of the person behind: store, queries, every cache with their data, their local storage', async () => {
    const assign = signedIn();
    const { remaining } = fakeCaches(['api-get', 'api-mutations', 'teamup-share-inbox', 'workbox-precache-v2']);

    await signOutLocally('/login');

    expect(useAuthStore.getState().user).toBeNull();
    expect(JSON.parse(localStorage.getItem('auth-storage') ?? '{}').state?.user).toBeNull();
    expect(queryClient.getQueryData(['messages', 'c1'])).toBeUndefined();
    // The app's own code stays cached; anything the person read or shared does not.
    expect(remaining()).toEqual(['workbox-precache-v2']);
    expect(localStorage.getItem('teamup.call.background.custom')).toBeNull();
    expect(localStorage.getItem('teamup.upload.chat:c1:a.mp4:1:2')).toBeNull();
    expect(localStorage.getItem('theme')).toBe('dark');
    expect(assign).toHaveBeenCalledWith('/login');
  });

  it('still signs out where Cache Storage does not exist', async () => {
    const assign = signedIn();
    vi.stubGlobal('caches', undefined);
    await signOutLocally();
    expect(useAuthStore.getState().user).toBeNull();
    expect(assign).toHaveBeenCalledWith('/login?next=%2Fchat%2Fc1');
  });

  it('clears and leaves once when several ways out fire together', async () => {
    const assign = signedIn();
    const { storage } = fakeCaches(['api-get', 'teamup-share-inbox']);
    await Promise.all([signOutLocally(), signOutLocally(), signOutLocally()]);
    expect(assign).toHaveBeenCalledOnce();
    expect(storage.delete).toHaveBeenCalledWith('teamup-share-inbox');
    expect(storage.delete.mock.calls.filter(([n]) => n === 'teamup-share-inbox')).toHaveLength(1);
    // And again later, harmlessly.
    await signOutLocally();
    expect(useAuthStore.getState().user).toBeNull();
  });

  it('does not send anybody already on a public page round to the login page again', async () => {
    signedIn();
    window.history.replaceState(null, '', '/login');
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => undefined);
    fakeCaches(['api-get']);
    await signOutLocally();
    expect(useAuthStore.getState().user).toBeNull();
    expect(assign).not.toHaveBeenCalled();
    expect(onlineManager.isOnline()).toBe(true);
  });
});
