# A meeting that opened early and emptied gets no reminder and no Join

Status: ready-for-human
Severity: High

Decision 23 says a scheduled meeting whose room emptied comes back until its scheduled end.
Decision 31 says a meeting already opened early is still reminded.
Neither holds once the row reaches `ended`, and the app itself makes that likely.

## Where

- `internal/calendar/calendar.go:859`: `SweepReminders` claims only `status IN ('scheduled', 'active')`.
- `web/src/features/calendar/layout.ts`, `joinTarget`: a meeting is `live` only when `scheduled` or `active`.
- `web/src/features/calls/CallsPage.tsx`, `joinable`: `ended` returns false.
- `web/src/features/calendar/EventDialog.tsx`: labels it ", finished".
- `internal/calendar/calendar.go:308`: anybody who was in the early session has the slot replaced by their real time, so it leaves their calendar and availability.

## Reproduction

1. Organizer schedules Planning at 10:00 to 11:00 with an invitee.
2. At 09:40 the organizer presses Join (offered from 30 minutes before), and is alone.
3. They leave, which posts `POST /calls/:id/end`, or `CallFrame` drops them after 2 minutes alone. Status becomes `ended`. `room_finished` does the same if they just close the tab.
4. At 09:55 the sweep runs.

Verified with a throwaway test: row `ended`, `SweepReminders(09:56)` returned 0, and the invitee's calendar item stays at 10:00 with status `ended`.
Result: nobody is reminded. At 10:00 the invitee sees "finished" with no Join on Calendar and on Calls, though `POST /calls/:id/token` would reopen it.
The same dead end hits a meeting in progress when everybody drops for longer than LiveKit's departure timeout.

## Suggested minimal fix

- A scheduled meeting that ended before its `scheduled_end` is still on. In the sweep, also claim `status = 'ended' AND standing_meeting_id IS NULL AND scheduled_end > now`.
- In `joinTarget` and `joinable`, treat `ended` with a scheduled end still ahead as joinable, using the scheduled start from the call row.
- In `loadCalls`, only replace the slot with real times once `scheduled_end` has passed. Do not label it finished before then.
- Test each with the row ended at 09:41.

## Comments

2026-11-07, senior developer:
The sweep in `internal/calendar/calendar.go` now also claims a meeting whose row is `ended`, since it only matches a start still ahead.
`loadCalls` replaces the slot with real times only once the scheduled end has passed, so somebody who looked in early keeps the slot on their calendar and availability.
`meetingStillOn` in `web/src/features/calendar/layout.ts` is the one statement of this: `joinTarget`, `EventDialog`, `CalendarGrid` and the Calls card use it, so Join is offered and it does not read as finished.
Verified: `TestAMeetingThatOpenedEarlyAndEmptiedIsStillRemindedOnce`, `TestAMeetingThatEmptiedEarlyHoldsItsSlotUntilItsEnd`, Vitest on `joinTarget` and `meetingStillOn`, and the real-browser test under "ticket 20" in `web/e2e/meetings.y-fixes.spec.ts`, which goes red with the fix taken out.
