# 34 - Due dates on task cards read "just now" or "3d ago"

Status: resolved
Type: bug
Severity: medium

## What is wrong

`web/src/features/tasks/TasksPage.tsx:447` renders the due date with `formatRelativeTime(task.dueDate)`.
`formatRelativeTime` (`web/src/shared/utils/cn.ts:27-39`) only handles the past: for a future date `diffMs` is negative, so `diffSecs < 60` is true and it returns "just now".
A date already past shows "2d ago" with no hint that it is overdue.

## How a user hits it

Create a task due next Friday. Its card says "just now". A task that was due three days ago says "3d ago", so overdue work is indistinguishable from work done three days ago. The detail page is fine because it uses `formatDate`.

## Simple fix

On the card use `formatDate(task.dueDate)` and add a destructive text class when the date is in the past and `task.status !== 'done'`. Do not change `formatRelativeTime`, other callers use it for past times.

## Answer

Task cards use `dueLabel` in `features/tasks/dueDate.ts`: "Due Nov 13", or "Overdue Nov 3" in red when past and not done.
`formatRelativeTime` is unchanged.
Tested in `dueDate.test.ts`.
A task due today counts as overdue from local midnight, matching the server's `due_date < NOW()`.

## Comments

Ticket 52 supersedes the overdue rule in the answer above: a due date set in the UI now means the end of that local day, so a task due today is not overdue until the day is over.
