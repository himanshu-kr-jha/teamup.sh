# 03 — Create a task from the UI

**What to build:** The "New Task" button on the board actually creates a task.

The button exists with an empty click handler, so today the only way to get a task into the system is the API. Creation is implemented and tested server-side, including assignment notifications.

**Blocked by:** 01 — Dialog and form primitives.

**Status:** ready-for-agent

- [ ] "New Task" opens a form for title, description, priority, type, assignee and due date
- [ ] Only the title is required; the rest fall back to sensible defaults
- [ ] The assignee list offers the organization's members
- [ ] A created task appears in the correct board column without a manual refresh
- [ ] Assigning it to someone else raises a notification for them
- [ ] Validation failures are reported in the form, not swallowed
