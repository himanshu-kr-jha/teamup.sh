// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest';
import { createBrowserRouter } from 'react-router-dom';
import { isFirstEntry, putEntryUnderneath } from './historyGuard';

// Against the real router: if an upgrade of React Router changes how it keeps its
// place in the browser's history, these fail, rather than Back quietly leaving a call.

const routes = [
  { path: '/calls', element: null },
  { path: '/calls/:id', element: null },
  { path: '/chat', element: null },
];

/** Waits for the router to settle on a path after Back. */
async function settles(router: ReturnType<typeof createBrowserRouter>, path: string) {
  for (let i = 0; i < 50 && router.state.location.pathname !== path; i++) await new Promise((r) => setTimeout(r, 10));
  return router.state.location.pathname;
}

let router: ReturnType<typeof createBrowserRouter> | undefined;
afterEach(() => {
  router?.dispose();
  router = undefined;
});

describe('an entry put underneath the first page', () => {
  it('is where Back goes, and the router keeps counting from it', async () => {
    window.history.replaceState(null, '', '/calls/abc');
    router = createBrowserRouter(routes);
    expect(isFirstEntry()).toBe(true);

    expect(putEntryUnderneath('/calls')).toBe(true);
    // Nothing moved on screen.
    expect(router.state.location.pathname).toBe('/calls/abc');
    expect(window.location.pathname).toBe('/calls/abc');
    expect(isFirstEntry()).toBe(false);

    // The next page the router opens comes after it, not over it.
    await router.navigate('/chat');
    expect((window.history.state as { idx: number }).idx).toBe(2);

    window.history.back();
    expect(await settles(router, '/calls/abc')).toBe('/calls/abc');
    window.history.back();
    expect(await settles(router, '/calls')).toBe('/calls');
    expect(isFirstEntry()).toBe(true);
  });

  it('is not put under a page that is not the first', async () => {
    window.history.replaceState(null, '', '/chat');
    router = createBrowserRouter(routes);
    await router.navigate('/calls/abc');
    expect(isFirstEntry()).toBe(false);
    expect(putEntryUnderneath('/calls')).toBe(false);
  });
});
