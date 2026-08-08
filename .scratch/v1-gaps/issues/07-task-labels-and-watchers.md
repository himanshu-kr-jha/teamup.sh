# 07 — Task labels and watchers

**What to build:** Tasks can be labelled and followed from the task detail page, and the board can be filtered by label.

Labels are fully implemented server-side with no UI. Watchers are different: the frontend already declares the endpoints, but **no watcher routes exist on the server**, so that half is genuinely unbuilt rather than merely unwired.

**Blocked by:** 01 — Dialog and form primitives.

**Status:** ready-for-agent

- [ ] The organization's labels can be listed, created and given a colour
- [ ] A label can be attached to and removed from a task, and shows on the card and detail page
- [ ] The board can be filtered to one label
- [ ] Adding a label a task already carries is harmless
- [ ] A user can follow and unfollow a task, and can see who is watching it
- [ ] Watchers are notified when the task changes, without notifying whoever made the change
- [ ] Watcher endpoints are organization-scoped like every other task route, with tests covering a cross-organization attempt
