# 10 — In-app notification centre

**What to build:** Something happening to you produces a notification you can see, count and clear.

Nothing in the codebase currently calls the notification creation path at all, so this slice has to wire at least one real trigger as well as implement storage and reads — otherwise there is nothing to demonstrate. It is blocked on both chat messages and tasks so that a mention and an assignment are available as triggers.

**Blocked by:** 06 — Chat message persistence and history; 07 — Task board and detail.

**Status:** ready-for-agent

- [ ] Being assigned a task creates a notification for the assignee
- [ ] Being mentioned in a channel message creates a notification for the mentioned user
- [ ] A user is not notified about their own action
- [ ] The bell shows an accurate unread count that survives a reload
- [ ] The notification list shows what happened, who caused it, when, and links through to the task or message
- [ ] Marking one notification read updates the count; marking all read clears it
- [ ] Archiving a notification removes it from the list
- [ ] A new notification appears live over the existing WebSocket connection without a refresh
- [ ] Notifications are only ever visible to their recipient
