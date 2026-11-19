// The one place TeamUp writes React Router's own history entries by hand.
//
// React Router (its browser history, @remix-run/router) keeps { usr, key, idx } in
// each entry's history.state and reads its position back from there on every
// change, so an entry written in that form is one it treats as its own. The
// public way to put an entry underneath the current one, navigating to the calls
// list and back, would show the calls list for a moment and shrink the call into
// the corner and out again.
//
// historyGuard.test.ts runs this against the real router, so an upgrade that
// changes the form fails there rather than quietly breaking Back.

interface RouterEntry {
  usr?: unknown;
  key?: string;
  idx?: number;
}

/** Whether the current entry is the first this tab's router has: Back from it leaves the app. */
export function isFirstEntry(history: History = window.history): boolean {
  const state = history.state as RouterEntry | null;
  return !!state && state.idx === 0;
}

/**
 * Puts an entry for `underneath` below the current one, which must be the first,
 * so that Back goes there instead of out of the app. The page on screen does not
 * change. Says whether it did.
 */
export function putEntryUnderneath(underneath: string, history: History = window.history, location: Location = window.location): boolean {
  const state = history.state as RouterEntry | null;
  if (!state || state.idx !== 0) return false;
  const here = location.pathname + location.search + location.hash;
  try {
    history.replaceState({ usr: null, key: Math.random().toString(36).slice(2, 10), idx: 0 }, '', underneath);
    history.pushState({ ...state, idx: 1 }, '', here);
    return true;
  } catch {
    // A browser limiting history writes: Back then leaves, as it always did.
    return false;
  }
}

/** The calls list, under a call's page opened as the first page of the tab. */
export const putCallsListUnderneath = () => putEntryUnderneath('/calls');
