# Make completed_at trustworthy

Status: resolved
Type: task
Blocked by: none

Re-saving status done overwrites completed_at. Creating a task as done never sets it. Tracking depends on this date, so fix it first. Also refuse a blank or over-long title on create (only PATCH validates it today).

## Answer

`UpdateTask` now keeps the first `completed_at` with `COALESCE`, and `CreateTask` stamps it when a task is created as done.
`CreateTask` trims the title and refuses a blank or over-long one with a 400, using the same `validTitle` rule as PATCH.
Tests: `internal/tasks/completion_test.go`.
Resolved together with ticket 15.
Existing rows with status done and no `completed_at` are not backfilled, so they never count as completed.
