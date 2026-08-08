# 05 — Chat channels

**What to build:** A user can create a channel, see the organization's channels in the sidebar, open one, and add someone to it.

The chat screen currently renders a hardcoded "Current Channel" placeholder because listing, creating and fetching channels all return nothing. This slice replaces the placeholder with real data and is the foundation the message history slice builds on.

**Blocked by:** 01 — Shared database test harness.

**Status:** ready-for-agent

- [ ] Creating a channel from the UI persists it and it appears in the sidebar without a manual refresh
- [ ] The sidebar lists the channels the signed-in user can see, and an organization with no channels shows an empty state rather than a placeholder channel
- [ ] Opening a channel shows its real name, type and description
- [ ] A member can be added to a channel and then sees that channel in their own sidebar
- [ ] Direct-message, group and public channel types are all creatable and are distinguished in the list
- [ ] Channels are scoped to the caller's organization; a channel id from another organization is not readable
- [ ] Requesting a channel that does not exist reports not-found rather than an empty channel
