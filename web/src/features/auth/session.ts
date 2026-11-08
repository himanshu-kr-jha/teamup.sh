import axios from 'axios';

/**
 * Only a 401 means the session is gone. A network failure, a 502 during a
 * deploy or an offline start say nothing about who you are.
 */
export function isSignedOut(err: unknown): boolean {
  return axios.isAxiosError(err) && err.response?.status === 401;
}

/**
 * Whether the browser still holds a session, for when local storage has forgotten
 * who is signed in, and says who it is. Plain fetch, because this asks a question
 * whose answer may well be no: the API client answers a 401 by trying to refresh and
 * then navigating to the login page itself.
 */
export async function restoreSession<User>(setUser: (user: User) => void): Promise<boolean> {
  try {
    const res = await fetch('/api/auth/me', { credentials: 'include' });
    if (!res.ok) return false;
    const body = await res.json();
    if (!body?.user) return false;
    setUser({ ...body.user, permissions: body.permissions ?? [] } as User);
    return true;
  } catch {
    return false;
  }
}

/**
 * Leaves the app with nothing of this person's behind. React Query keys carry no
 * user id, and neither do the service worker's cached responses, so the next
 * person to sign in on this browser would otherwise be served them.
 */
export async function endLocalSession(deps: {
  clearStore: () => void;
  clearQueries: () => void;
  clearOfflineCache: () => Promise<unknown>;
  goToLogin: () => void;
}) {
  deps.clearStore();
  deps.clearQueries();
  await deps.clearOfflineCache().catch(() => undefined);
  deps.goToLogin();
}
