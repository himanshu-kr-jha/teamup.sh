# Dashboard UI for employee, manager and admin

Status: resolved
Type: task
Blocked by: 02, 03, 04

Overview, By person, Scrum, History and Contribution tabs in /tasks, per spec.md. Replaces StandupView. Also fix: the board has no Blocked column, so blocked tasks vanish from it; List and Board buttons show no active state and contain empty icon placeholders.

## Answer

Built in `web/src/features/tasks/dashboard/` and `TasksPage.tsx`, with the board moved to `BoardView.tsx` and `StandupView.tsx` deleted.
Tabs come from the scope the server returns, and the tab, team, person, saved scrum and date range live in the URL.
The board gained a Blocked column, filter entry, phone pill and card action, and List and Board have real active states.
Logic tests are in `dashboard/logic.test.ts`. The views were checked in Chromium against a mocked API at 1280, 375 and 320 wide, light and dark.
