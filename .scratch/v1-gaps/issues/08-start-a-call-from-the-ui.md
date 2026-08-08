# 08 — Start a call from the UI

**What to build:** The "New Call" button starts a call and invites people to it.

The button has an empty click handler. Behind it, creating a call already provisions the room, records the call and its participants, and lists it in history — none of which is reachable from the UI today.

**Blocked by:** 01 — Dialog and form primitives.

**Status:** ready-for-agent

- [ ] "New Call" opens a form for a title, call type and who to invite
- [ ] The invitee list offers the organization's members
- [ ] Starting a call adds it to the list as active, without a manual refresh
- [ ] Invitees are notified and can join from the call list
- [ ] The starter is recorded as the host
- [ ] Ending a call moves it to history with its duration
- [ ] The dead "join" and overflow controls on existing call rows either work or are removed
