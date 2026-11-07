# Attendance marks people absent from days it could not know about

Status: ready-for-human
Severity: Low to Medium

Absent is derived on read from the meeting as it is now (`internal/calls/standing.go:384` to `430`), so these show false absences.

## (a) Created after the day's slot

`standing.go:393` compares dates only.
Verified with a throwaway test: a meeting created now with a slot 3 hours ago lists everybody as `absent` today, though the meeting did not exist then.
So an admin creating the morning scrum at 14:00 sees the whole company absent.
Fix: skip the day when `start` is before `m.CreatedAt` and it was not held.

## (b) Editing the weekdays rewrites history

`m.runsOn(d.Weekday())` uses today's weekdays for every past date.
If an admin adds Saturday, every past Saturday since creation becomes a day with everybody absent. If they remove Friday, every past Friday that nobody joined disappears.
Decision 29 protects the start of a held occurrence and nothing else.
Read from the code at `standing.go:386` and `393`. Not run.
Fix: record when the schedule last changed, for example `schedule_changed_at`, and derive absence only from then on for days not held. Alternatively keep a small history of weekdays.

## (c) Absent before it has started

The "only once it is due" check at `standing.go:402` is on the not-held branch only.
If somebody presses Join at 08:30 for a 09:30 scrum and connects, the day is held and recorded, so at 08:31 everybody else reads `absent`.
Fix: for a held day whose `scheduledStart` is after now, leave out people who have not come, or mark them with a status such as `expected`.

## (d) The page's default range uses the viewer's date

`web/src/features/calls/standing/standing.ts`, `defaultAttendanceRange`, uses the browser's date and always sends `from` and `to`, though the server already works out today in the meeting's zone when given neither (`standing.go:660`).
A manager in New York at 22:00 on 7 Nov, reading a 09:00 Asia/Kolkata scrum held on 8 Nov, does not see today's day until they change the dates.
Fix: compute the default end date in `meeting.timeZone` with `Intl.DateTimeFormat`, once the meeting has loaded.

## Comments

2026-11-07, senior developer:
(a) and (b) Migration 0024 adds `standing_meeting_schedules`, and attendance reads the schedule in force on each date, rather than a `schedule_changed_at`, so removing a weekday keeps past absences as well as adding one not inventing them.
A slot earlier than the moment its schedule was set has nobody absent.
(c) Before a held day's start, only people who came are listed.
(d) `defaultAttendanceRange` takes the meeting's zone, and the page waits for the meeting list before its first read.
Verified: `TestAMeetingCreatedAfterTheDaysSlotMarksNobodyAbsent`, `TestEditingTheWeekdaysDoesNotRewriteHistory`, `TestSomebodyEarlyDoesNotMakeEverybodyElseAbsentBeforeTheStart`, Vitest on `defaultAttendanceRange`.
`newScrumWorld` now fixes its clock, because (c) showed those tests passed or failed by the time of day they ran.
The calendar still draws past standing slots from the current schedule. It is outside this ticket and states nothing about absence.
