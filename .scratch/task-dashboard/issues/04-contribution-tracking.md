# Who did what, and how much

Status: resolved
Type: task
Blocked by: 01

GET /tasks/contributions and GET /tasks/contributions/:userId, per spec.md decision 6.

## Answer

`GET /tasks/contributions` and `GET /tasks/contributions/:userId` are in `internal/tasks/contributions.go`.
Former assignees come from `task_activities` compared as text, never cast, so `<nil>` rows already in deployed databases cannot break the query.
Tests: `internal/tasks/contributions_test.go`.
