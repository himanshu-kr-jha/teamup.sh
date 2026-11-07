# Your own calendar shows who else came to a standing meeting

Status: ready-for-human
Severity: Medium (privacy)

Decisions 15 and 27 say only an admin, or a manager for their people, may see who else came.
`GET /calls/:id` deliberately withholds it (`internal/calls/routes.go:382`).
The calendar gives it away.

## Where

- `internal/calendar/calendar.go:260`: `ARRAY(SELECT i.user_id ... FROM call_participants i ...)` for every call.
- `internal/calendar/calendar.go:289`: `InviteeIDs: invitees` is set for all sources, and the `standingID != nil` case at line 292 does not clear it.
- `internal/calls/webhook.go:183`: `syncParticipant` puts everybody who connected into `call_participants`, so that list is the attendance list.
- `web/src/features/calendar/EventDialog.tsx`: shows it as "Invited: Bob, Carol".

## Reproduction

Verified with a throwaway test.

1. Ann and Bob, both plain members, each attend today's occurrence.
2. Ann calls `GET /api/calendar?userId=<ann>&from=...&to=...`.
3. The item with `"source":"standing"` has `"inviteeIds":[<ann>,<bob>]`.

Ann can do this every day she attends, and so get attendance for people she has no authority over, up to the moment she reads it.
Any item seen through a manager's view of Ann also lists people that manager does not answer for.

## Suggested minimal fix

In the `standingID != nil` case set `item.InviteeIDs = nil`, and `OrganizerID = nil` while there, since an occurrence's creator is not an organizer.
Add a test that an attended occurrence on your own calendar names nobody else.

## Comments

2026-11-07, senior developer:
`loadCalls` clears `inviteeIds` and `organizerId` on an occurrence, and keeps the list only internally for the log's rule.
Verified: `TestAnAttendedStandingMeetingOnYourCalendarNamesNobodyElse` and the real-browser test under "ticket 22 and 31", which goes red with the fix taken out.
