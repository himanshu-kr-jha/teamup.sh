# Long titles lose every notification; moving a meeting can fail or remind twice

Status: ready-for-human
Severity: Medium for (a), Low to Medium for (b), Low for (c)

## (a) A long title means nobody is told

`validateMeeting` allows 500 characters (`internal/calendar/calendar.go:530`), and neither `ScheduleDialog` nor `meetingProblem` limits it.
`notifications.title` is `VARCHAR(255)` (`migrations/0001_initial_schema.up.sql:414`).
`"You were invited to " + title` over 255 fails the insert in `CreateNotification`, which returns before the frame and the push.
`s.notify` only logs a warning (`calendar.go:843`).

Verified with a throwaway test: a 300 character title returned 201 and the invitee had no notifications at all.
The same applies to `moved`, `cancelled` and `reminder`, and to `"You were added to " + taskTitle` (`internal/tasks/tasks.go:1607`), since a task title can be 500.

Suggested minimal fix: truncate the title to 255 characters by rune, with an ellipsis, inside `notifications.CreateNotification` and `NotifyMany`, so every caller is covered.

## (b) A meeting with a deactivated invitee cannot be moved

`resolveInvitees` refuses the whole list if anybody on it is not active (`calendar.go:560`).
`ScheduleDialog` always sends the existing `inviteeIds`.

Verified with a throwaway test: invite Gone and Stay, deactivate Gone, then `PATCH /meetings/:id` with the new times and both invitees answers 400 "everybody invited must be in this organization".
The organizer has no way of telling who is meant, because the dialog shows such a person as "Somebody".

Suggested minimal fix: in `UpdateMeeting`, validate only ids not already on the meeting, and drop inactive ones silently.

## (c) Changing only the end sends a second reminder

`calendar.go:707` counts a changed end as moved, and line 732 then clears `reminded_for`.

Verified with a throwaway test: sweep at 09:56 for a 10:00 meeting, `PATCH` with `endsAt` only, sweep at 09:57, and the invitee has 2 reminders.
Decisions 16 and 31 say once.

Suggested minimal fix: remove the `reminded_for` clear. `reminded_for IS DISTINCT FROM scheduled_start` already re-arms when the start changes.

## Comments

2026-11-07, senior developer:
(a) `fitTitle` in `internal/notifications/notifications.go` shortens a title to 255 characters by rune with an ellipsis, in `CreateNotification` and `NotifyMany`, so every caller is covered, tasks included.
(b) `resolveEditedInvitees` checks only people new to the meeting, and drops anybody already on it who has left. `notify` and the reminder sweep skip anybody deactivated.
(c) `reminded_for` clears only when the start changes. It is not simply removed, because `TestAMeetingMovedAwayAndBackIsRemindedAgain` is a behaviour worth keeping.
Verified: `TestALongTitleIsShortenedRatherThanLosingTheNotification`, `TestALongMeetingTitleStillReachesEverybody`, `TestAMeetingWithSomebodyDeactivatedOnItCanStillBeMoved`, `TestSomebodyDeactivatedIsNotReminded`, `TestChangingOnlyTheEndDoesNotRemindAgain`.
