# Members upload their own profile picture

Status: ready-for-human
Severity: Low
Asked for by: the user, "allow member to upload their profile pic".

## Comments

2026-11-08, orchestrator: decision 55.
`internal/org/avatar.go`, migration 0025 (`user_avatars`), `web/src/features/settings/ProfilePicture.tsx`, `web/src/shared/squareImage.ts`, `web/src/shared/components/UserAvatar.tsx`.
Verified: `internal/org/avatar_test.go`, and end to end: a landscape picture comes back 256 by 256 and shows on the person's messages to a colleague, then goes back to initials.
Not shown yet: pictures on call tiles, task assignees and the conversation list.
