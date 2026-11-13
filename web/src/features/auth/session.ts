import axios from 'axios';
import { onlineManager } from '@tanstack/react-query';
import { useAuthStore } from '@/shared/store/authStore';
import { queryClient } from '@/shared/api/queryClient';
import { loginPathFor } from './nextPath';

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
 * Every Cache Storage cache that can hold something of the signed-in person's:
 * the service worker's API caches (named in vite.config.ts) and what another app
 * shared and was never sent (features/share/inbox.ts, public/push-sw.js).
 */
export const PERSONAL_CACHES = ['api-get', 'api-mutations', 'teamup-share-inbox'];

/**
 * Local storage of the person's own: the picture they put behind them on camera
 * and the notes that let an interrupted upload carry on. Settings of the browser
 * itself, such as the theme, stay.
 */
const PERSONAL_STORAGE_PREFIXES = ['teamup.call.background', 'teamup.upload.'];

/** Deletes the caches above; there is nothing to do where Cache Storage is missing. */
export async function clearPersonalCaches(): Promise<void> {
  if (typeof caches === 'undefined') return;
  await Promise.all(PERSONAL_CACHES.map((name) => caches.delete(name).catch(() => false)));
}

function clearPersonalStorage() {
  try {
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i);
      if (key && PERSONAL_STORAGE_PREFIXES.some((p) => key.startsWith(p))) localStorage.removeItem(key);
    }
  } catch {
    // Storage that cannot be read holds nothing to leave behind either.
  }
}

let ending: Promise<void> | null = null;

/**
 * Leaves the app with nothing of this person's behind. React Query keys carry no
 * user id, and neither do the service worker's cached responses, so the next
 * person to sign in on this browser would otherwise be served them.
 *
 * Every way out comes through here: signing out, changing the password, a refresh
 * the server refused and a 401 from /auth/me. Several of those fire together when
 * a session dies under a page full of queries, so a call made while one is under
 * way joins it rather than clearing and navigating twice.
 */
export function endLocalSession(deps: {
  clearStore: () => void;
  clearQueries: () => void;
  clearOfflineCache?: () => Promise<unknown>;
  goToLogin: () => void;
}): Promise<void> {
  if (ending) return ending;
  ending = (async () => {
    try {
      deps.clearStore();
      deps.clearQueries();
      clearPersonalStorage();
      await (deps.clearOfflineCache ?? clearPersonalCaches)().catch(() => undefined);
      deps.goToLogin();
    } finally {
      ending = null;
    }
  })();
  return ending;
}

/** Pages anyone may be on signed out; sending them to sign in from there would loop. */
const PUBLIC_PATHS = ['/login', '/wizard', '/status', '/join'];

export function onPublicPath(pathname: string): boolean {
  return PUBLIC_PATHS.some((p) => pathname === p || pathname.startsWith(p + '/'));
}

/**
 * endLocalSession with the app's own store and query cache, for every caller in
 * or out of React. `to` is where to sign in again; by default the login page,
 * remembering the page they were on. Nobody is moved off a public page.
 */
export function signOutLocally(to?: string): Promise<void> {
  return endLocalSession({
    clearStore: () => useAuthStore.getState().logout(),
    clearQueries: () => {
      // Emptying the cache makes every mounted query fetch again, and the session
      // is gone by then: those would all come back 401 on the way out.
      if (!onPublicPath(window.location.pathname)) onlineManager.setOnline(false);
      queryClient.clear();
    },
    goToLogin: () => {
      if (onPublicPath(window.location.pathname)) return;
      window.location.assign(to ?? loginPathFor(window.location.pathname, window.location.search));
    },
  });
}
