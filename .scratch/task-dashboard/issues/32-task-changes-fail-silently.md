# 32 - Moving, editing, reassigning or deleting a task fails with no message, and Delete has no confirmation

Status: resolved
Type: bug
Severity: high

## What is wrong

- `web/src/features/tasks/TasksPage.tsx:114-117` `updateTaskMutation` has no `onError`.
- `web/src/features/tasks/TaskDetailPage.tsx:95-102` `updateTaskMutation` and `:104-110` `deleteTaskMutation` have no `onError`. These drive the status select, priority select, title and description edit, the reassign select (`:318`) and the due date input (`:356`).
- The server does refuse these: `UpdateTask` / `DeleteTask` return 403 or 400 (`internal/tasks/tasks.go:987-996`, `validateUpdates`, and the delete authority rule). The board deliberately shows tasks the viewer cannot act on (`can.edit/reassign/delete`, `abilities.ts`), and `TaskCard` still offers "Move to ..." and "Mark Done" on those cards (`TasksPage.tsx` card menu).
- `TaskDetailPage.tsx:184` Delete fires `deleteTaskMutation.mutate()` straight from the menu item, with no confirm, and the task leaves every board for everybody.

## How a user hits it

A manager opens the card menu on a task held by an org admin and picks "Mark Done": nothing happens, no toast, the card does not move. On the detail page the status select snaps back with no reason. One mis-click on Delete removes a task with no way back from the UI.

## Simple fix

Add `onError: (err) => toast.error(apiErrorMessage(err, 'Could not update that task.'))` (and a delete equivalent) to the three mutations, using `useToast` and `apiErrorMessage` already imported on the detail page. Wrap the Delete action in a `window.confirm('Delete this task?')`.

## Answer

The board's and detail page's task mutations now raise a toast with the server's reason, and Delete asks `window.confirm` first.
Not unit testable without a DOM: verified in Chromium with a mocked 403 on PATCH and DELETE, which showed the toasts and one confirm prompt.
The icon-only buttons on the card and detail page also got accessible labels.
