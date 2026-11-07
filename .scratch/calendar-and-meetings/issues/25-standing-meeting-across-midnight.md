# A standing meeting that crosses midnight becomes two rooms

Status: ready-for-human
Severity: Medium

The occurrence is chosen by today's date in the meeting's zone (`internal/calls/standing.go:254` to `259`), not by which slot is running or next.
`durationMinutes` goes up to 480 and any time of day is accepted, so the slot can cross midnight.

## Reproduction

Both verified with a throwaway test using `SetClock`.

**Late in a meeting.** Meeting at 23:30 UTC for 60 minutes.
Ann joins at 23:40 on 9 Nov and gets `standing-b2a42a30`, dated 2026-11-09.
Bob joins at 00:10 on 10 Nov and gets `standing-c003a61a`, dated 2026-11-10.
They are in different rooms, in the same meeting, and cannot hear each other.

**Early for a meeting.** Meeting at 00:05 UTC.
Ann presses Join at 23:58 on 9 Nov. The occurrence is dated 9 Nov with a start of 00:05 that morning.
Attendance for 9 Nov reads `"minutesLate":1433`, and 10 Nov shows her absent if she stays in that room.

This also affects a zone far from the people in it: an admin in Kolkata can pick a meeting at 23:45 in another zone.
`attendeeFrom` cuts an open session at the end of the date, and `openForJoin` (`internal/calls/calls.go:1475`) stops reopening at midnight too.

## Suggested minimal fix

Pick the occurrence by slot, in one shared function used by `JoinStandingMeeting` and `openForJoin`:
1. If yesterday's slot in the zone is still running (`now < start + duration`), and it runs on that weekday, use yesterday's date.
2. If tomorrow's slot starts within the early join window, say 30 minutes, and it runs on that weekday, use tomorrow's date.
3. Otherwise use today.

The alternative is to refuse a time and duration that cross midnight in `applyTo`. That still leaves the early case for a slot just after midnight.

## Comments

2026-11-07, senior developer:
`occurrenceDay` in `internal/calls/standing.go` picks the day of the slot as this ticket suggested, with a 30 minute early window, and `JoinStandingMeeting` and `openForJoin` share it.
Attendance counts an open session to the end of a slot that runs past midnight, and the calendar's `joinTarget` offers Join until then.
Verified with `SetClock`: `TestASlotCrossingMidnightIsOneRoom`, `TestAnOccurrenceCrossingMidnightReopensForItsLinkAndCountsItsWholeLength`, `TestPressingJoinJustBeforeASlotJustAfterMidnightIsThatSlot`.
Not verified in a browser, as that needs the clock at midnight.
