# 51 - The chosen theme (dark, or System) is forgotten on every page load

Status: resolved
Type: bug
Severity: medium

Found while taking dark-mode screenshots of the dashboard in a real browser.

## What is wrong

The theme is saved to `localStorage['theme']` by the header toggle (`web/src/features/layout/Header.tsx:47`) and the Settings, Appearance select (`SettingsPage.tsx:121`), but nothing reads it back at startup.

- `Header.tsx:19` starts its own state at `'light'` whatever is stored.
- Nothing sets the `dark` class on `<html>` on load (`grep classList` finds only the two click handlers).
- "System" is stored but never honoured: `prefers-color-scheme` is only consulted at the moment somebody picks System in Settings.

## Exact reproduction

1. Sign in, click the moon icon in the header. The app goes dark.
2. Reload the page (or follow any link that does a full navigation, such as the one after login or logout).
3. The app is light again. `localStorage.theme` is still `"dark"`.
   The header then shows a moon icon (the next click would "switch to dark"), so the icon agrees with the screen but not with the saved choice.

Also: set Settings, Appearance to System on a machine whose OS is dark. Reload. The app is light.

## Simple fix

Before first render (a small inline script in `web/index.html`, so there is no flash), read `localStorage.theme` and toggle `document.documentElement.classList` the same way the Settings handler does, treating a missing value or `system` as `prefers-color-scheme`.
Initialise the Header's `theme` state from the document's class instead of a constant.

Acceptance: reload with `theme=dark` in localStorage and `<html>` has class `dark` before the first paint. The E2E screenshots helper `useTheme` in `web/e2e/screenshots.spec.ts` can then drop its click on the toggle.

## Answer

A small inline script in `web/index.html` sets the `dark` class before the first paint, treating a missing value or `system` as `prefers-color-scheme`.
The rule is `isDarkTheme` in `web/src/shared/theme.ts`, and `theme.test.ts` runs the actual script out of `index.html` against it, so the two copies cannot drift.
There is no Content-Security-Policy anywhere in the app or `nginx.conf` today. If one is added it has to allow this script.

The header toggle holds no state now: `useIsDark` reads the class on `<html>`, so it also follows a change made in Settings, which the ticket did not ask for but would have left the icon wrong.
Header and Settings both save through `applyTheme`.
With System selected, the OS preference is read at load and when the setting changes, not live while the page is open.

A person who never chose a theme now follows their OS, as the ticket specifies, so somebody on a dark OS sees dark for the first time.
The screenshots helper no longer clicks the toggle.
