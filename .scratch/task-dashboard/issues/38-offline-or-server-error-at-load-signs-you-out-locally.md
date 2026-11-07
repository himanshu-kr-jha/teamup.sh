# 38 - Any failure of /auth/me at page load clears the session and kills the websocket

Status: resolved
Type: bug
Severity: medium

## What is wrong

`web/src/features/auth/AuthProvider.tsx:30-40` `refreshUser` catches every error and calls `setUser(null)`.
That includes a network failure, an offline start of the installed PWA, a proxy 502 during a deploy, or a 500.
A real 401 is already handled by the axios interceptor (`client.ts` refresh then redirect), so this catch only adds the wrong cases.

With `isAuthenticated` false, `WSProvider.tsx` (the `else` branch of its connect effect) closes the socket as intentional and never reconnects, so there is no live chat, presence or call ringing until a manual reload. `SettingsPage` also loses the user, and `useOrganizationAccess` treats them as a non-admin.

## How a user hits it

Open the app on a phone with a flaky connection, or reload during a deploy. `/auth/me` fails once, the app renders but is silently deaf and the admin menu disappears. Later requests succeed, so it looks logged in.

## Simple fix

In the catch, only `setUser(null)` when `axios.isAxiosError(err) && err.response?.status === 401`; otherwise keep the persisted user.

## Answer

`refreshUser` only clears the user on a real 401 (`isSignedOut` in `features/auth/session.ts`), and keeps the persisted user otherwise.
Tested in `session.test.ts`.
