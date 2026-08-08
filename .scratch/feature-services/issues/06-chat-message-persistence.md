# 06 — Chat message persistence and history

**What to build:** Messages survive. Sending one stores it, reopening a channel shows what was said before, and editing, deleting and read state all work.

Note the current behaviour, because it is misleading: the WebSocket send path is fully implemented and broadcasts to other connected clients, but the call that would store the message does nothing. Messages therefore appear live for anyone currently watching and are gone for good on reload. The same is true of edits, deletes and last-read updates.

**Blocked by:** 05 — Chat channels.

**Status:** ready-for-agent

- [ ] A sent message is stored and is still there after a reload
- [ ] Opening a channel loads recent history, oldest-to-newest, with paging back through older messages
- [ ] The optimistic message shown on send is reconciled with the stored one rather than duplicated
- [ ] Editing a message persists the new content and marks it edited
- [ ] Deleting a message removes it from the channel for everyone
- [ ] Marking a channel read persists, and unread counts in the sidebar reflect it across reloads
- [ ] A second connected client sees a new message live, and sees the same history when it reloads
- [ ] Messages are scoped to their channel and organization; history cannot be read for a channel the caller is not a member of
