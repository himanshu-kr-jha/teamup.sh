# 02 — Create a channel from the UI

**What to build:** The "New Channel" button in the chat sidebar actually creates a channel.

The button exists and does nothing — its click handler is empty. Everything behind it is implemented and tested: creating a channel makes the creator its owner, and the new channel appears in the sidebar for anyone entitled to see it.

**Blocked by:** 01 — Dialog and form primitives.

**Status:** ready-for-agent

- [ ] "New Channel" opens a form asking for a name, a type and an optional description
- [ ] Creating a channel adds it to the sidebar without a manual refresh, and opens it
- [ ] The creator can administer the channel immediately
- [ ] A public channel is visible to the whole organization; a private one only to its members
- [ ] A duplicate or invalid name is reported in the form rather than silently failing
- [ ] Cancelling closes the dialog and creates nothing
