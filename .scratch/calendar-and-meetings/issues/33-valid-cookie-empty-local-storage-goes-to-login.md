# A valid session with empty local storage lands on the login page

Status: ready-for-human
Severity: Low
Found by: browser tester, reported to the orchestrator and not filed at the time.

## Steps

1. Sign in, so the session cookies are set.
2. Clear local storage, or lose `auth-storage` some other way, leaving the cookies.
3. Open any page of the app.

## What happens

`requireAuth` in `web/src/app/router.tsx` read only what local storage remembered, so it sent somebody who is signed in to `/login`, losing where they were going.

## Comments

2026-11-07, senior developer: `requireAuth` now asks the server through `restoreSession` in `web/src/features/auth/session.ts` before deciding, using plain `fetch`, because the API client answers a 401 by navigating to the login page itself.
Somebody with no session still goes to `/login?next=...`.
Verified by `restoreSession` tests in `session.test.ts` and "stays where it was going" in `web/e2e/meetings.y-fixes.spec.ts`.
While a first loader waits the router now draws the page background rather than nothing, so this wait is not a blank white page in the dark theme.
