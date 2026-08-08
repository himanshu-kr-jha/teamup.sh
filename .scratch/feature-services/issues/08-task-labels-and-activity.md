# 08 — Task labels and activity feed

**What to build:** Tasks can be labelled, and a task's detail page shows the history of what happened to it.

Label creation already works; listing labels, attaching and detaching them, and reading a task's activity trail do not.

**Blocked by:** 07 — Task board and detail.

**Status:** ready-for-agent

- [ ] The organization's labels can be listed, with their names and colours
- [ ] A label can be attached to a task and shows on the task card and detail page
- [ ] A label can be removed from a task
- [ ] Attaching a label a task already has is harmless rather than an error
- [ ] Filtering the board by label returns only tasks carrying it
- [ ] A task's detail page shows an activity trail in reverse-chronological order
- [ ] Status changes, reassignment, and comments each produce an activity entry naming who did it and when
- [ ] Labels and activity are scoped to the caller's organization
