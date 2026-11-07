# 52 - A task due today is shown as overdue today, on its card and in the Overdue count

Status: resolved
Type: bug
Severity: medium

Known defect, now reproduced end to end. Test: `web/e2e/dashboard.spec.ts`, "9. due today is not overdue" (red).

## What is wrong

A due date is a calendar day, but it is stored and compared as an instant:

- `CreateTaskDialog.tsx` turns the picked day into local midnight: `new Date(dueDate + 'T00:00:00').toISOString()`.
- The server decides overdue with `due_date < now`, so from 00:00:01 on the due day the task is overdue.
- The client trusts the server's flag (`dueLabel` in `web/src/features/tasks/dueDate.ts` uses `task.overdue` when present, and `new Date(task.dueDate) < now` otherwise).
- Ticket 34's answer calls this intended ("A task due today counts as overdue from local midnight"). It is not what people mean: nothing is late until the day is over.

## Exact reproduction

1. Sign in as any employee. Tasks, New Task.
2. Title "Due today", Assignee: yourself, Due date: today's date. Create.
3. Board tab: the card reads "Overdue Nov 6" in red (today is Nov 6).
4. Overview tab: Overdue tile is 1 (red). The task is listed twice, under Pending and under Overdue, both "Overdue Nov 6".
5. A manager's dashboard, scrum and By person show the same for this person.

Expected: card says "Due Nov 6", Overdue tile is 0, until the end of Nov 6 in the viewer's day.
Actual: overdue from the first second of the due day.

Screenshot: /tmp/claude-1000/-home-himanshu-kumar-jha-Documents-Ongoing-Projects-teamUp/47fd8d20-9a9e-48c5-8ba0-78ed94fb07ed/scratchpad/e2e-shots/defects/due-today-shows-overdue.png
(The test also asserts the server's `totals.overdue` is 0 for her.)

## Simple fix

Treat the due date as a day. Decide in one place, on the server (`internal/tasks` dashboard overdue predicate, and the `overdue` flag on task rows): overdue means `due_date::date < current_date` in the organization's or caller's time zone, not `due_date < now()`.
The dialog already sends local midnight, so the server needs the caller's offset or the date part only.
Update `dueDate.test.ts` and the Go dashboard tests that pin the old rule, and ticket 34's answer.

## Answer

Fixed in the UI, not on the server as this ticket first proposed: a picked day now means the end of that local day.
`endOfDueDay` in `web/src/features/tasks/dueDate.ts` turns it into local 23:59:59.999, and both places that set a due date use it (`CreateTaskDialog`, `TaskDetailPage`).
The server's `due_date < now` is unchanged. The date shown is read back in local time, so it stays the picked day, tested in four time zones and across a clock change.
The task page only offers the date input while no date is set, so there is no prefilled value that could disagree.

Accepted limits:

- Tasks saved before this fix keep their midnight value, so one due today or later still turns overdue from the first second of its due day. Nothing migrates them.
- The end of the day is the end of the day of whoever set the date. A viewer in another time zone can see a different date, and overdue flips at the setter's midnight, not theirs.
