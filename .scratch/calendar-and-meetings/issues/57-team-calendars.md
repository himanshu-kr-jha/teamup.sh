# A manager sees the calendars of the people under them

Status: ready-for-human

## What

Asked for: on the Calendar page somebody sees their own calendar, as today, and can also view the calendars of the people who work under them, that is the people on the teams they manage.

Before this, the page had a picker of everybody in the organization.
Anybody saw a colleague's times as busy, focus or open with no titles, and somebody with authority saw the detail (decision 4).
Every calendar opened that way also offered the same clicks as your own, such as scheduling by clicking an empty slot.

## Decisions

Decision 64 in `spec.md`.
In short:

- **Who:** the people `HasAuthorityOver` already gives a manager for tasks, so sub-teams count: Mia, who runs Backend with Platform nested under it, sees Pat on Platform.
  An administrator sitting on a team is not under its manager, as for tasks.
  `meetings.all` ("read any call's log and attendance") opens anybody, as it already showed anybody's detail.
- **Anybody else is refused** with 403, not given the free and busy outline any more.
  A colleague's free and busy time is still where it is needed, in the scheduling dialog's availability and conflicts.
  It is 403 rather than 404 because the person is plainly in the organization.
- **What a manager sees:** what the person sees, titles of meetings and blocks, organizer and invited people, and the call log only where its own rule allows.
  That is what decision 4 already gave them, and knowing what a report's time goes to is why a manager opens it.
- **Read-only:** no Block time, no clicking an empty slot, and an item's details have no Edit, Remove, Cancel, Join or guest link.
  The server never let anybody change another person's block (404 by owner), and that is now tested for managers, administrators and the owner.
  Schedule stays, with the person already invited, because the meeting is the manager's own and lands on their calendar.

## Built

- `internal/tasks/tasks.go`: `Reports`, the teams somebody runs with only the people `HasAuthorityOver` allows, each once; no new statement of the chart.
- `internal/calendar/calendar.go`: `Calendar` refuses without authority; `Viewable` builds the picker's groups, active people only, with "Everybody else" for `meetings.all`.
- `internal/calendar/routes.go`: `GET /calendar/people`.
- `web/src/features/calendar/CalendarPage.tsx`: the picker grouped by team, shown only when it has anybody; a banner naming whose calendar it is, read-only, with Back to mine; a refusal with Open my calendar for a link to somebody you cannot see.
- `web/src/features/calendar/people.ts`: whose calendar the address means, and how to name them.
- `EventDialog.tsx` and `CalendarGrid.tsx`: read-only on somebody else's calendar.
- `web/src/shared/components/ui/select.tsx`: `SelectLabel` for the team names.

## Comments
