# Save completed scrums as immutable snapshots

Status: resolved
Type: task
Blocked by: 02

Migration plus POST /scrums, GET /scrums, GET /scrums/:id, per spec.md decisions 3, 4 and 5. New tables must be added to testsupport tablesToClean.

## Answer

Migration `0015_scrums` adds `scrums`, `scrum_participants` and `scrum_entries`, and `POST /scrums`, `GET /scrums` and `GET /scrums/:id` are in `internal/tasks/scrum.go`.
Saving is one transaction, and there is no edit or delete route.
Tests: `internal/tasks/scrum_test.go`.
