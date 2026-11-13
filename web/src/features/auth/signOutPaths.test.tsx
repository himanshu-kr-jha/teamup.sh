// @vitest-environment happy-dom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { AxiosError, type InternalAxiosRequestConfig } from 'axios';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, authApi } from '@/shared/api/client';
import { queryClient } from '@/shared/api/queryClient';
import { useAuthStore } from '@/shared/store/authStore';
import { fakeCaches } from '@/test/fakeCaches';
import { AuthProvider } from './AuthProvider';
import { ChangePasswordPage } from './ChangePasswordPage';

// Every way a session ends must leave the browser as signing out does, not only
// the sign-out button: these drive each one the way the app meets it.

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const someone = { id: 'u1', name: 'Ann', email: 'ann@example.com', roles: [], orgId: 'o' };
let root: Root | null = null;
let assign: ReturnType<typeof vi.spyOn>;
let caches: ReturnType<typeof fakeCaches>;
const realAdapter = api.defaults.adapter;

/** A server that has forgotten this session: every request, a refresh included, is a 401. */
function serverSaysSignedOut() {
  api.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
    const response = { status: 401, statusText: '', data: {}, headers: {}, config };
    throw new AxiosError('unauthorized', 'ERR_BAD_REQUEST', config, undefined, response);
  };
}

beforeEach(() => {
  useAuthStore.getState().setUser(someone);
  queryClient.setQueryData(['messages', 'c1'], [{ id: 'm1', text: 'secret' }]);
  window.history.replaceState(null, '', '/chat/c1');
  assign = vi.spyOn(window.location, 'assign').mockImplementation(() => undefined);
  caches = fakeCaches(['api-get', 'teamup-share-inbox', 'workbox-precache-v2']);
});

afterEach(async () => {
  await act(async () => root?.unmount());
  root = null;
  api.defaults.adapter = realAdapter;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

async function render(ui: React.ReactNode) {
  const el = document.createElement('div');
  document.body.appendChild(el);
  root = createRoot(el);
  await act(async () => root!.render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>));
  return el;
}

const until = (check: () => boolean) => vi.waitFor(() => expect(check()).toBe(true));

function expectNothingLeft() {
  expect(useAuthStore.getState().user).toBeNull();
  expect(queryClient.getQueryData(['messages', 'c1'])).toBeUndefined();
  expect(caches.remaining()).toEqual(['workbox-precache-v2']);
}

describe('ending a session other than by signing out', () => {
  it('a refresh the server refuses clears everything local and goes to sign in', async () => {
    serverSaysSignedOut();
    await expect(api.get('/tasks')).rejects.toBeInstanceOf(AxiosError);
    expectNothingLeft();
    expect(assign).toHaveBeenCalledWith('/login?next=%2Fchat%2Fc1');
  });

  it('a 401 from /auth/me at start clears everything local', async () => {
    serverSaysSignedOut();
    await render(
      <AuthProvider>
        <span />
      </AuthProvider>
    );
    await until(() => caches.remaining().length === 1);
    expectNothingLeft();
  });

  // A guest on a meeting link has no account: /auth/me answers 401 there too.
  // Clearing the query cache then made the guest page fetch its meeting again
  // and start its form over, so the name they had typed was gone and Join
  // stayed greyed out.
  it('a visitor who was never signed in keeps what their page holds', async () => {
    useAuthStore.getState().logout();
    queryClient.setQueryData(['guest', 't1'], { title: 'Weekly sync' });
    window.history.replaceState(null, '', '/join/t1');
    serverSaysSignedOut();
    await render(
      <AuthProvider>
        <span />
      </AuthProvider>
    );
    await new Promise((r) => setTimeout(r, 50));
    expect(queryClient.getQueryData(['guest', 't1'])).toEqual({ title: 'Weekly sync' });
  });

  it('changing the password clears everything local before signing in again', async () => {
    vi.spyOn(authApi, 'changePassword').mockResolvedValue({ data: {} } as never);
    const el = await render(
      <MemoryRouter>
        <ChangePasswordPage />
      </MemoryRouter>
    );
    const type = (id: string, value: string) => {
      const input = el.querySelector<HTMLInputElement>(`#${id}`)!;
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
      input.dispatchEvent(new Event('input', { bubbles: true }));
    };
    await act(async () => {
      type('currentPassword', 'temporary-one');
      type('newPassword', 'a-long-new-password');
      type('confirmPassword', 'a-long-new-password');
    });
    await act(async () => el.querySelector('form')!.requestSubmit());
    await until(() => assign.mock.calls.length > 0);
    expectNothingLeft();
    expect(assign).toHaveBeenCalledWith('/login');
  });
});

