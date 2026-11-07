# 13 - Task history records "<nil>" and changes that did not happen

Status: resolved
Type: bug

## What is wrong

`UpdateTask` writes one `task_activities` row per submitted field when `beforeValue(before, key) != fmt.Sprintf("%v", value)`.

1. `beforeValue` only knows title, description, status, priority, taskType, assigneeId. For `dueDate`, `startDate`, `storyPoints`, `position`, `parentTaskId` it returns `""`, so every PATCH that touches them logs a change, even when the value is identical. A card drag that sends `position` writes an activity row each time (`old ""`, `new "3"`). Re-saving the same due date also logs.
2. Clearing a field sends JSON null; the new value is `fmt.Sprintf("%v", nil)` = `"<nil>"`. Unassigning (`{"assigneeId": null}`) stores new_value `<nil>` literally. The history screen shows "<nil>", and the spec derives "were assignee" from `task_activities.old_value/new_value` of `assigneeId`, so any query that casts those to uuid will fail on `<nil>`.
3. Times are formatted with `%v` ("2026-11-10 00:00:00 +0000 UTC"), not RFC3339, so the same date logged via two clients looks different.

## Where

- `internal/tasks/tasks.go:1033-1043` (activity loop), `:1067-1085` `beforeValue` (default `""`).
- Null path: `validateUpdates` leaves `nil` in `updates` (`tasks.go:~690, ~720`), then `Sprintf("%v", nil)`.

## How to reproduce

In `internal/tasks/tasks_test.go`: create a task with `dueDate` D. PATCH `{"dueDate": D}` (same value) then `GetActivities`: a `dueDate` row exists with old `""` (should be none). PATCH `{"assigneeId": null}` on an assigned task: the newest `assigneeId` activity has `NewValue == "<nil>"` (should be empty). PATCH `{"position": 5}` twice: two rows.

## Simple fix

Make `beforeValue` return the real prior value for all whitelisted fields (RFC3339 for times, empty for nil pointers), format the new value the same way (nil -> `""`, `time.Time` -> RFC3339, uuid -> string), and skip when equal.

## Answer

`beforeValue` now knows every whitelisted field, and the new `activityValue` writes both sides the same way: nil as nothing and times as RFC3339 UTC, so saving an unchanged value records no row.
Tests: `TestSavingTheSameValueRecordsNoHistoryForAnyField`, `TestClearingAFieldRecordsNothingAsTheNewValue` and `TestDatesInHistoryAreRFC3339WhateverZoneTheyArrivedIn` in `internal/tasks/tasks_test.go`.
Existing `<nil>` rows are not rewritten, so old history can still display them.
