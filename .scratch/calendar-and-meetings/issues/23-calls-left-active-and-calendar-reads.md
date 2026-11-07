# A call never closed shows as busy for weeks, and nothing ever closes it

Status: ready-for-human
Severity: Medium for (a), Low for (b) and (c)

## (a) Stale active calls cover the calendar

`internal/calendar/calendar.go:317`: an unscheduled call with no `ended_at` runs from `started_at` to now.
`internal/calendar/calendar.go:265` lets it through for any range.
Spec decision 24 says such rows exist already ("stayed active for ever"), and `0017` to `0021` leave them `active` (checked on a throwaway database taken through the migrations with one such row).
New ones keep appearing: LiveKit retries a webhook only a few times, so a `room_finished` sent while the app restarts for a deploy is lost, and nothing reconciles afterwards.

Verified with a throwaway test: an `active` call started 40 days ago, with one invitee who never joined.
`GET /calendar/availability` for the last 2 days returned `{"kind":"busy","startsAt":"2026-09-28...","endsAt":"2026-11-07..."}`.
So everybody ever rung for such a call is a solid block on the week view up to this minute, and "In a meeting" in the scheduling dialog today.

Suggested minimal fix:
1. A migration closing unscheduled calls that are `active` and older than a day: `status = 'ended'`, `ended_at = COALESCE(last session or joined time, started_at)`.
2. A periodic sweep in the same loop style as `RunReminders`: for calls `active` for more than a few minutes, ask LiveKit `ListRooms` and treat an absent room as `room_finished`. It must skip the sweep if the call to LiveKit fails.
3. In `loadCalls`, cap the end of an unclosed call for somebody who never joined, for example `started_at + RingWindow`.

## (b) No index for the new reads

`call_participants` has only its primary key `(call_id, user_id)` (`migrations/0001_initial_schema.up.sql:368`).
`loadCalls` filters `p.user_id = ANY($2)` (`calendar.go:263`), which is a sequential scan on every calendar read, every availability read and every conflict check.
Calendar pages poll each minute.
Add `CREATE INDEX idx_call_participants_user ON call_participants (user_id, call_id)`.
It is small today, and grows by one row per person per scrum.

## (c) Hanging up can lose to the webhook it just caused

`internal/calls/routes.go:562` deletes the room and then calls `MarkCallEnded` at line 566, as does `DeclineCall` at `routes.go:277`.
Deleting the room makes LiveKit send `room_finished`.
If `finishRoom` reaches `MarkCallEnded` first, the handler's call matches no `active` row and returns `ErrNotFound`.
Then `POST /end` or `POST /decline` answers 404 "failed to end call", and on decline `finishRoom` has already written "Missed call" rather than "Call declined" (`webhook.go:345`, `noteFor`).
Found by reading the order of operations and confirming `MarkCallEnded` returns not found on a second call. The window is short and I did not observe it with real timing.

Suggested minimal fix: in both handlers mark the row ended and note it before deleting the room, and treat `ErrNotFound` from `MarkCallEnded` as already done.

## Comments

2026-11-07, senior developer:
(a) Migration 0022 closes unscheduled calls `active` for over a day, with their sessions, participants, pending knocks and a `left` line on the trail.
`SweepVanishedRooms` in `internal/calls/media.go` runs every 5 minutes on every replica, ends a call left alone for 10 minutes whose room is gone through the same `finishRoom` as the webhook, and changes nothing if LiveKit cannot be asked.
`loadCalls` caps somebody who never joined an unclosed call at the 45 second ring window.
(b) Migration 0022 adds `idx_call_participants_user`.
(c) `endRecordThenRoom` closes the record first and the room second, and treats "already ended" as done, in both `EndCall` and `DeclineCall`.
Verified: `TestARoomThatIsGoneEndsACallWhoseWebhookWasLost`, `TestACallNobodyClosedDoesNotCoverTheCalendarOfSomebodyWhoNeverJoined`, `TestHangingUpSurvivesTheWebhookItCauses`, `TestDecliningSaysDeclinedEvenIfTheWebhookArrivesFirst` (a fake LiveKit delivers `room_finished` inside the room deletion, so the race happens every time), and `TestClosingStaleCallsLeavesLiveOnesMeetingsAndADownAlone` for the migration.
Not verified: the sweep against a real LiveKit restart with a lost webhook. `liveKit.RoomNames` is a thin call to `ListRooms`, which `TestEndCallReallyClosesTheRoomOnLiveKit` exercises through `EndCall`.
A first attempt at (c) left `DeclineCall` unchanged, and its test passed anyway because that path did not reach the fake LiveKit. Taking the fix out of `endRecordThenRoom` showed that, and now both tests go red without it.
