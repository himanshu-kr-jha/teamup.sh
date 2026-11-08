# An organization has its own colour and logo

Status: ready-for-human
Severity: Low
Asked for by: the user, "create custom theme settings for the site for the organisation and logo, and default can be current".

## Comments

2026-11-08, orchestrator: decision 50.
`internal/org/branding.go` (public `GET /api/branding` and `/api/branding/logo`, admin `PUT /api/org/branding`), `web/src/shared/branding.ts`, `web/src/features/branding`, and `web/src/features/admin/AppearanceTab.tsx`.
Verified: `internal/org/branding_test.go`, `web/src/shared/branding.test.ts` (including that index.html paints the remembered colour exactly as the app does), and the end-to-end test, which saves teal and a logo, sees them signed out on the sign-in page and on a reload with the branding request blocked, then goes back to the default.
