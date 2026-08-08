# 09 — Call history and detail

**What to build:** The Calls screen shows the calls that have actually happened or are happening now, and one can be opened to see who was on it.

Starting, joining and ending a call are already implemented and write to the database, but listing and fetching calls are not, so the Calls screen has nothing to show and a finished call leaves no trace.

**Blocked by:** 01 — Shared database test harness.

**Status:** ready-for-agent

- [ ] The Calls screen lists the organization's calls, most recent first, with an empty state when there are none
- [ ] Active calls are distinguished from ended ones, and an active call can be joined from the list
- [ ] Filtering by status, and by the channel a call belongs to, returns the right subset
- [ ] Each entry shows title, type, who started it, when it started, and duration for ended calls
- [ ] Opening a call shows its participants and when each joined and left
- [ ] A call that has just ended appears in history without a manual refresh
- [ ] Requesting a call that does not exist, or one from another organization, reports not-found
