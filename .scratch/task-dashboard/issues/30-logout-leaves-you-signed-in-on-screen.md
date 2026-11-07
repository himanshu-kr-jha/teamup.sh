# 30 - Log out leaves you inside the app, with the last person's data cached

Status: resolved
Type: bug
Severity: high

## What is wrong

The header "Log out" item only clears the zustand auth store.
It does not navigate anywhere and it does not clear the react-query cache.

- `web/src/features/layout/Header.tsx:81` calls `logout` from `AuthProvider`.
- `web/src/features/auth/AuthProvider.tsx:50-56` calls `POST /auth/logout` and then `logoutStore()`. Nothing else.
- `web/src/features/layout/Layout.tsx:19-23` only redirects for `mustChangePassword`. It has no `!isAuthenticated` branch, and the router `requireAuth` loader (`web/src/app/router.tsx:15`) does not re-run on in-app navigation.
- `web/src/main.tsx:11-19` sets `staleTime` to 5 minutes and nothing ever calls `queryClient.clear()` on logout (only the change-password flow does, `SettingsPage.tsx:144`).

## How a user hits it

1. Sign in on a shared machine, open Chat and Tasks.
2. Click Log out. The page looks unchanged, the same channels and tasks are still on screen. Only the websocket quietly closes.
3. Go to `/login` and sign in as somebody else (the login page navigates inside the SPA, no reload). `['channels']`, `['tasks']`, `['notifications']`, `['availability']` and the rest are served from the previous user's cache for up to 5 minutes, because react-query keys carry no user id.

## Simple fix

In `AuthProvider.logout`, after the store reset do `queryClient.clear()` and `window.location.assign('/login')`, the same way `SettingsPage.tsx:144-146` already does for a password change.

## Answer

`AuthProvider.logout` now clears the store, the query cache and the service worker's `api-get` cache, then does a full navigation to `/login`.
The logic is `endLocalSession` in `features/auth/session.ts`, tested in `session.test.ts`.
