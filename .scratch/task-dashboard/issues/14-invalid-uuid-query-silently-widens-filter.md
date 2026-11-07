# 14 - A malformed id in a list filter is dropped, widening the result

Status: resolved
Type: bug

## What is wrong

`queryUUID` returns `nil` for an unparseable value, and `nil` means "no filter". So `GET /tasks?assigneeId=garbage`, `?projectId=junk`, `?labelId=junk` return every task in the organisation. `?scope=team&teamId=all` drops the team narrowing and returns every team the caller manages (for an org admin who runs no team: the whole organisation). An unknown `scope` value is also ignored and returns the whole org list. A client that sends "all" or an id with a typo gets a plausible-looking but wrong, wider answer instead of an error. The dashboard spec says the server decides scope and a client "cannot get one by asking"; this lets a malformed request silently become the widest one, and new dashboard handlers will inherit the helper.

`ListCalls` has the same pattern for `channelId` (`internal/calls/routes.go:48-53`): a bad id lists all of the caller's calls.

## Where

- `internal/tasks/routes.go:76-86` `queryUUID`; used at `routes.go:93-98`.
- `internal/tasks/routes.go:~106-112` unknown `scope` falls through.
- `internal/calls/routes.go:48-53`.

## How to reproduce

`internal/tasks/routes_test.go`: seed two tasks with different assignees. `GET /tasks?assigneeId=not-a-uuid` returns 200 with both tasks; expected 400. `GET /tasks?scope=bogus` returns 200 with all; expected 400. `GET /tasks?scope=team&teamId=all` as an admin with no team returns the whole org; expected 400.

## Simple fix

Make `queryUUID` return `(*uuid.UUID, bool)`; on a non-empty unparseable value respond 400 `invalid <key>`. Reject `scope` not in `"" | team | mine`. Same one-line change in `ListCalls`.

## Answer

`ListTasks` uses the existing `optionalQueryUUID` helper (from `dashboard.go`), so a malformed `assigneeId`, `projectId`, `labelId` or `teamId` is a 400, and `queryUUID` is gone.
A `scope` other than empty, `team` or `mine` is a 400, and `ListCalls` refuses a malformed `channelId` the same way.
The web client maps its own "all" to no parameter before calling, so it is unaffected.
Tests: `TestAMalformedListFilterIsRefusedRatherThanIgnored` in `internal/tasks/routes_test.go:372`, `TestAMalformedChannelFilterIsRefused` in `internal/calls/calls_test.go:939`.
