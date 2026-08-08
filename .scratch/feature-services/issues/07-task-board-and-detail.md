# 07 — Task board and detail

**What to build:** The full life of a task: create it, find it on the board, open it, change it, delete it.

Creating a task already works and writes to the database, but nothing can read tasks back, so a created task vanishes from the user's point of view. Listing, fetching, updating and deleting are all unimplemented.

**Blocked by:** 01 — Shared database test harness.

**Status:** ready-for-agent

- [ ] A created task appears on the board in the column matching its status, without a manual refresh
- [ ] The board lists the organization's tasks and shows an empty state when there are none
- [ ] Filtering by status and searching by title return the right subset
- [ ] Opening a task shows its real title, description, status, priority, assignee and due date
- [ ] Changing status or priority persists and is reflected on the board
- [ ] Reassigning a task persists
- [ ] Deleting a task removes it from the board and from its detail route
- [ ] Requesting a task that does not exist, or one from another organization, reports not-found
