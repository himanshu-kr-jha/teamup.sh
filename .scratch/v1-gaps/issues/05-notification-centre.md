# 05 — Notification centre

**What to build:** The bell shows how many notifications you actually have, and opens a list you can read and clear.

The badge is currently a hardcoded number and there is no notifications UI at all. Everything behind it works: being assigned a task or mentioned in a channel already stores a notification and pushes it over the open WebSocket, and endpoints exist for listing, counting, marking read and archiving. Nothing displays any of it.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] The badge shows the real unread count and disappears at zero
- [ ] Opening the bell lists notifications newest first, showing what happened, who caused it and when
- [ ] Each entry links through to the task or message it refers to
- [ ] Opening one marks it read and updates the count
- [ ] "Mark all read" clears the badge
- [ ] Archiving removes an entry from the list
- [ ] A notification arriving while the tab is open appears without a refresh
- [ ] An empty state is shown rather than a blank panel
