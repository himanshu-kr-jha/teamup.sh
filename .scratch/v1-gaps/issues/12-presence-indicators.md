# 12 — Presence indicators

**What to build:** You can see who is online.

The presence service is implemented and its endpoints are registered; the WebSocket already carries presence updates. Nothing in the interface shows any of it — the green dot next to the signed-in user in the sidebar is decoration.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] A member's avatar shows whether they are online, away or offline
- [ ] Status changes appear live over the existing WebSocket, without a refresh
- [ ] Presence is shown consistently wherever people appear: message authors, member lists, task assignees
- [ ] A user can set themselves away or busy, and it persists across a reload
- [ ] Closing the last tab eventually shows the user as offline rather than leaving them online forever
- [ ] Presence never leaks across organizations
