# A meeting that runs over shows as extended on the calendar

Status: ready-for-human
Severity: Low
Asked for by: the user, "if the meeting is extended, it should be reflected on its calendar as extended meeting time".

## What happened

A meeting stayed at its scheduled time on the calendar until the call ended, so one running 20 minutes over looked finished, and the person in it looked free.

## Comments

2026-11-08, orchestrator: decision 46.
`internal/calendar/calendar.go` gives the item `scheduledEndsAt` and `running`, and the page draws the overrun striped, says **Extended** where there is room, and the details say "Scheduled until 10:30, extended to 10:42 (12 min over)" or "still going (12 min over so far)".
The page grows a running block with its own clock between reads.
Verified: `internal/calendar/extended_test.go` (running past its end, grows, busy in availability; ended late; left before the end; ended early; never came), shown red first; unit tests in `layout.test.ts`; and `web/e2e/meetings.y-calendar.spec.ts` with a real LiveKit call moved past its end in the database.
Not verified: a meeting really left running for its whole length, and webhooks being down during an overrun.
