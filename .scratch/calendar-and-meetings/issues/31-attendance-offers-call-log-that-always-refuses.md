# The attendance page offers a call log to people who can never read it

Status: ready-for-human
Found by: ticket 09 (`web/e2e/meetings.spec.ts`, test "defect 31"). The test is red until this is fixed or the test is changed to match a decision.

## Steps

1. An admin makes a standing meeting, and an employee (Alice) joins it, so there is a day with a call.
2. As Alice, open **Calls**, then **Attendance** on that meeting (`/calls/standing/<id>/attendance`).
3. Press the clock button **Show the call log for <day>** on a day.

## What happens

The page offers the button to Alice, and pressing it always ends in a dialog that says "You do not have access to the log of this call."
The server answers `GET /calls/<id>/log` with 404 for her every time, because a standing meeting has no members (decision 27) and she is not an admin or a manager over somebody who came.
Seen in `employee__attendance-log-refused__375__light.png` and `employee__attendance-log-refused__1280__light.png`
(in `/tmp/claude-1000/-home-himanshu-kumar-jha-Documents-Ongoing-Projects-teamUp/47fd8d20-9a9e-48c5-8ba0-78ed94fb07ed/scratchpad/e2e-shots-meetings/`).

## What the spec says

Decision 27: only an org admin or owner, or a manager over somebody who came, reads an occurrence's log.
Decision 15: an employee sees only their own attendance row.
Ticket 08 says "Attendance draws whatever the server returns" and nothing is hidden by role, which is how this arose.
The spec does not say the button must be hidden, so this may be decided the other way; if so, change the test.

## Notes

A control whose only possible outcome is a refusal is a wart the person meets on every visit.
The page already knows the viewer's role (the admin buttons on Calls are hidden for non-admins), so it can show the button to admins, owners and managers only.
A manager is refused when nobody they answer for came, so for managers the refusal text is still needed.

## Comments

2026-11-07, senior developer:
Rather than guessing from roles, the server says: attendance days, calendar items, the call list and `GET /calls/:id` carry `canReadLog`, computed by the log's own rule.
So a manager over nobody who came is not offered it either, which role checks could not have done.
The button is hidden on the attendance page, the calendar's dialog, the Calls card and inside a standing room.
Verified: "defect 31" is green and now also checks an admin still gets a working log, plus `TestAttendanceSaysWhetherTheReaderMayOpenThatDaysLog`, `TestTheCallListAndTheCallSayWhetherYouMayOpenTheLog`.
