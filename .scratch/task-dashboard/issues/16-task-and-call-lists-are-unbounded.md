# 16 - Task board and call list return every row ever, on every refresh

Status: resolved
Type: bug

## What is wrong

`ListTasks` has no LIMIT and does not exclude old finished work: each request returns every non-deleted task in the org (with full descriptions), then runs `attachLabels` and `attachAbilities` (two more queries sized to the page) over all of them. The board and the planned dashboard refetch on focus and every 30 seconds. At 50 people creating a few tasks a week each, a year in that is on the order of 10-25k rows per request per open tab. `ListCalls` is the same: no LIMIT, plus a correlated `COUNT(*)` per row.

## Where

- `internal/tasks/tasks.go:~176-290` `ListTasks` (query ends `ORDER BY t.position, t.created_at DESC` with no LIMIT).
- `internal/calls/calls.go:~815-840` `ListCalls` (`ORDER BY c.created_at DESC`, no LIMIT).

## How to reproduce

Seed 5000 tasks (or 2000 calls), call `GET /tasks` / `GET /calls`: all rows come back in one response. Expected: bounded page.

## Simple fix

Cap `ListCalls` at 200 and add an optional `limit` (max 500). For tasks, exclude `status = 'done'` rows with `completed_at` older than 30 days unless `status=done` or `status=all` is asked for explicitly, and add the same capped `limit`. Keep the dashboard's own queries range-bounded.

## Answer

`ListTasks` and `ListCalls` are capped at 1000 rows, with no pagination API.
For tasks the rows that survive are chosen first and then ordered for the board: every open task ranks above every done one whatever its age, and within each the most recent `COALESCE(completed_at, created_at)` stays.
Calls keep the newest by `created_at`.
This differs from the Simple fix above (30 day cutoff, optional `limit`, cap of 200), by decision: a plain cap that a team of 50 never notices.
Tests: `TestTheTaskListCapDropsOldFinishedWorkBeforeOpenWork` in `internal/tasks/tasks_test.go:1684`, `TestTheCallListKeepsOnlyTheNewest` in `internal/calls/calls_test.go:967`.
The response does not say it was cut, so past the cap old done work is simply absent.
