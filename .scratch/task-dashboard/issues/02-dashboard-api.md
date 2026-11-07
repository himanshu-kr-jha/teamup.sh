# Role-scoped dashboard API

Status: resolved
Type: task
Blocked by: 01

GET /tasks/dashboard, per spec.md. Includes people with no work. Excludes deactivated users. Fixes: admin who runs no team must get the whole organization.

## Answer

`GET /tasks/dashboard` is in `internal/tasks/dashboard.go`.
`resolveAudience` is the one helper for who a caller answers for, and it filters team scope through `authorityFacts`.
An admin always gets the whole organization, whether or not they run a team.
Tests: `internal/tasks/dashboard_test.go`.
