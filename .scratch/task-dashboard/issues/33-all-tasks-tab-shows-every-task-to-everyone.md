# 33 - "All tasks" returns the whole organisation's tasks to any employee

Status: needs-info
Type: bug
Severity: medium

## What is wrong

`spec.md` says the server decides scope and an employee sees only their own work.
`GET /tasks` does the opposite when `scope` is omitted:

- `internal/tasks/routes.go:108-113` `switch c.Query("scope")` has cases for `team` and `mine` and no default, so no `Involving` / `ManagedBy` filter is applied and `ListTasks` (`internal/tasks/tasks.go:196`) returns every non-deleted task in the org.
- `web/src/features/tasks/TasksPage.tsx:155` shows the "All tasks" tab to everybody, and `:101-103` sends no scope for it.

## How a user hits it

Any employee clicks "All tasks" and reads every colleague's tasks, including titles and descriptions, with their `can` flags. Calling `GET /api/tasks` directly does the same.

## Simple fix

Give the switch a `default` that, for a caller who is not an org admin, sets `filters.Involving = &cl.UserID`. Optionally hide the "All tasks" tab for non-admins. Check `internal/tasks/routes_test.go` for tests that rely on the unscoped list.
