# 36 - Newly assigned or changed tasks do not appear on the board for up to five minutes

Status: resolved
Type: bug
Severity: medium

## What is wrong

- `web/src/main.tsx:11-19` sets `staleTime: 5 min` and `refetchOnWindowFocus: false`.
- `TasksPage.tsx:~93` `['tasks', ...]` and `TaskDetailPage.tsx:87` `['task', id]` use those defaults and no websocket event touches them.
- The server does send `notification.new` with `data.taskId` when you are assigned a task (`internal/tasks/tasks.go:47-55`, `internal/notifications/notifications.go:357`) but `NotificationBell.tsx:55-58` invalidates only the notification queries.

## How a user hits it

A manager assigns you a task. The bell shows it, you click "Tasks" (or are already on it), and the board does not list it. Re-opening the page within five minutes still serves the cache. The detail page of a task someone else just moved shows the old status.

## Simple fix

In the `notification.new` handler in `NotificationBell.tsx`, when `msg.payload.data?.taskId` is present also `invalidateQueries({ queryKey: ['tasks'] })` and `['task', taskId]`. Set `refetchOnWindowFocus: true` on the two task queries.

## Answer

`notification.new` with a `taskId` now invalidates the board, that task and the dashboard (`refreshTasksFor`).
The task queries use `staleTime: 0` as well as `refetchOnWindowFocus: true`, because focus alone does nothing under the 5 minute default.
The service worker's API cache moved from StaleWhileRevalidate to NetworkFirst, or every refetch over HTTPS would return the previous answer.
Tested in `features/tasks/liveQuery.test.ts` with the app's real query defaults.
