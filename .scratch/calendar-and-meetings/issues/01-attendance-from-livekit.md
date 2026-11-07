# Attendance and audit trail come from LiveKit

Status: resolved

Add `POST /api/livekit/webhook`, verified with the LiveKit signature, and configure LiveKit to call it in prod and dev compose.
Store each session and an append-only `call_events` log (decisions 6, 10).
Stop treating a token as attendance. Keep `call_participants` correct for existing readers.
Handle duplicate and out-of-order webhooks, and a person with two devices.
Add `GET /calls/:id/log` (decision 11).
Screen share started and stopped come from track webhooks.

## Comments

### 2026-11-06 - Tester: shapes the tests for tickets 01 to 05 assume

Failing tests exist for tickets 01 to 05.
The spec fixes behaviour and routes but not response shapes, so the tests assume the following.
Tell me where a shape differs and I will change the test, or change the code, whichever is cheaper.
Everything is HTTP plus the existing tables, so internal names are free.
Files: `internal/calls/{livekit_helpers,attendance,knock,standing}_test.go`, `internal/calendar/{harness,calendar}_test.go`, `internal/tasks/added_to_test.go`.

**Wiring points (the only places a test touches production structure)**
- `internal/calls/livekit_helpers_test.go`, `webhookRouter`: the webhook is registered by the same `RegisterRoutes` on a group with NO session middleware. If you register `/api/livekit/webhook` with a separate function, change that one line.
- The calls `Service` is built with `Config{LiveKitAPIKey: "devkey", LiveKitAPISecret: ...}` and no URL. The webhook must verify against those two config values, so no new config is needed to test it. Calls `Notifier` is a test wrapper embedding `*notifications.Service` that also records `PushToUsers`; it still satisfies the interface if you add `Notify` to it.
- `internal/calendar/harness_test.go`: the package does not exist, so `buildRouter` and `sweepReminders` are stubs that call `t.Fatal`. Replace their bodies once the package exists. `buildRouter` must mount the routes on `/api` behind middleware setting `org_id`, `user_id`, `user_roles`, with the real `notifications.Service` as notifier. `sweepReminders(now)` runs one pass of the reminder job as if it were `now` and returns how many reminders it sent.
- `internal/testsupport/db.go` `tablesToClean` needs every new table added, or tests leak rows into each other.

**Webhook (01)**
- Signed the way the LiveKit server SDK verifies it: `Authorization: <JWT>` with `sha256` claim of the raw body, content type `application/webhook+json`, body is protojson `WebhookEvent`. Bad signature, missing header, wrong key, wrong secret, signature of another body: 401.
- Participant identity is the user id. A room or identity we do not know is answered 2xx and ignored, so LiveKit does not retry forever.
- Duplicates are detected by the webhook event `id`. A session is keyed by the LiveKit participant `sid`: a new sid for the same user is a second session; a left that arrives before its joined is held and closed when the joined arrives.
- Joined time is `participant.joined_at`, left time is the event `created_at` (both whole seconds). Screen share is a `track_published` / `track_unpublished` with `track.source = SCREEN_SHARE`; camera tracks add no event.
- `call_participants.joined_at` is set only by a webhook join, never by the token. `left_at` stays empty while any session of that person is open.

**Log (01)** `GET /api/calls/:id/log` returns `{"events":[{"id","type","userId","actorId","at"}],"sessions":[{"userId","joinedAt","leftAt"}]}`.
- `type` is one of `knocked`, `admitted`, `denied`, `joined`, `left`, `screen_share_started`, `screen_share_stopped`. `userId` is who it is about, `actorId` is who did it (admitted and denied: the member who answered). `leftAt` is null for an open session.
- PUT, PATCH, DELETE and POST on the log path answer 404 or 405.

**Knock (02)**
- Token for a non-member: 403 with `{"error": "...", "code": "knock_required", "canKnock": true}`. Another organization: 404.
- `POST /calls/:id/knock` returns 2xx with `{"request": {"id","userId","status"}}`, status `pending`. A repeat while pending returns the same request (one pending request per person). A knock soon after a denial: 429 with a `Retry-After` header; after 60 seconds it makes a new request. A knock on an ended call: 409 or 410.
- `GET /calls/:id/requests` returns `{"requests":[{"id","userId","status"}]}`, status one of `pending`, `admitted`, `denied`, `ended`. Members only (non-members and the knocker get 403 or 404). Pending requests become `ended` or disappear when the call ends.
- `POST /calls/:id/requests/:rid/admit|deny`: 200 or 204. A non-member, including an org admin who is not in the call, gets 403 or 404. Answering an answered request is not 2xx.
- "Connected" for pings means a webhook session that is open at that moment.
- WebSocket frames are asserted by prefix `call.knock` with `requestId` in the payload: to each connected member when somebody knocks; to the knocker with `status` `admitted` or `denied`; and to the other connected members when somebody answers (to clear the prompt). Exact type names are free.
- With nobody connected, the organizer and invitees each get a push through `Notifier.PushToUsers`, or a row in `notifications`, either counts. Non-members get nothing.
- The tests age a call by moving every timestamp in any table with a `call_id` column back, so the knock request table must have `call_id` and a timestamp column. The test fails loudly if no such table exists.

**Standing meetings (03)**
- `POST /standing-meetings` body `{"name","timeOfDay":"HH:MM","timeZone":"IANA","weekdays":[1..7]}`, weekdays are ISO (Monday is 1, Sunday is 7). 201 with `{"standingMeeting": {...}}` or the bare object. List: `{"standingMeetings":[...]}`, archived ones excluded. Invalid name, time, zone or weekdays: 400. Manager or member: 403.
- `DELETE` archives (200 or 204); joining an archived one is 404 or 410.
- `POST /standing-meetings/:id/join` returns 200 `{"callId","roomName","token","date":"YYYY-MM-DD"}`, where `date` is the occurrence date in the meeting's zone.
- The occurrence is a row in `calls`. The test that moves an occurrence to yesterday shifts every `date` and `timestamptz` column of that `calls` row by one day, so the occurrence date must be stored on the `calls` row (or derivable from its timestamps).
- The occurrence ends (`status = ended`) after the last session leaves and the room finishes (`participant_left` then `room_finished`), and joining again the same day sets it live again, same row.
- Attendance `GET /standing-meetings/:id/attendance?date=` or `?from=&to=` always returns `{"days":[{"date","scheduledStart","people":[{"userId","name","status":"present|absent","firstJoinAt","lastLeaveAt","minutesPresent","minutesLate"}]}]}`. Minutes are whole numbers, overlapping devices are not counted twice, early arrival is 0 minutes late. A date the meeting does not run on lists nobody. A person who joined the organization after that date, or who is deactivated, is not listed as absent. A manager asking for a team they do not run: 403 or 404.

**Calendar (04)**
- `GET /calendar?userId=&from=&to=` returns `{"items":[{"id","kind":"busy|focus|open","source","title","startsAt","endsAt"}]}`. Without authority the item has `kind` and times only: no `title`, and no title text anywhere in the body. Meetings, calls and standing meetings are all `busy`. Another organization's user: 404. Missing, reversed, unparseable or over-long (a year) range: 400.
- `POST /calendar/blocks` body `{"kind":"focus|open","title","startsAt","endsAt"}`, 201 with `{"block": {"id"}}` or bare. `PATCH` and `DELETE` by anybody but the owner, including admins and managers, are 403 or 404. Longer than thirty days, end not after start, unknown kind: 400.
- `POST /meetings` body `{"title","startsAt","endsAt","inviteeIds":[]}`, 201 with `{"meeting":{"id"},"conflicts":[{"userId","kind"}]}`. `PATCH` returns the same `conflicts` field. A conflict is an overlapping focus block or meeting; open slots, back-to-back, and free people are not conflicts. Conflicts carry no titles. An invitee from another organization or an unknown id is refused (400, 404 or 422).
- `PATCH` and `DELETE /meetings/:id`: organizer and org admin only; invitee, the invitee's manager and outsiders get 403 or 404.
- `GET /calendar/availability?userIds=a,b&from=&to=` returns `{"people":[...]}` with items of `kind` `busy|focus|open` and never a title, even for an admin.
- Past calls: an invitee who connected sees `call_participants.joined_at` to `left_at`; an invitee who never connected sees the call's own `started_at` to `ended_at`.
- Notifications are rows in `notifications` whose `type` contains `invit`, `mov`, `cancel` and `remind` respectively; the organizer is not notified of their own action. A reminder fires when `now` is within 5 minutes before the start, is claimed once across concurrent sweeps, is not sent for a cancelled meeting, and a moved meeting is reminded again at its new time.

**Tasks (05)**
- Dashboard: `tasks.addedTo` (list) and `counts.addedTo` per person, and `totals.addedTo`. Open tasks only (not `done`, not deleted), watcher who is not the assignee, assigned to somebody else or to nobody.
- Contributions list `addedTo` per person; the detail has an `addedTo` array. Same range rule as `involved`. `completed` is unchanged.
- Saved scrum: `participants[].counts.addedTo`, and the added-to task appears in `entries` for that person. A saved scrum does not change afterwards.
- A second add of someone already on the task does not notify again (the route today notifies every time).

**Not testable without a seam, left to ticket 09 or a clock injection**
- The midnight crossing of an occurrence date cannot be tested as such: there is no clock to freeze. The tests prove the date follows the meeting's zone using zones 25 hours apart, and a new day by moving the old row back a day. A `Service.SetClock` (or a `now func() time.Time` field) would let a real midnight test be written.
- "No double ringing when a scheduled meeting starts" is not tested: how a meeting starts is not defined yet.

### 2026-11-07 - Senior developer: built, and what the frontend needs

Migration `0017_call_attendance`: tables `call_sessions` and `call_events`, column `call_participants.answered_at`.
Code: `internal/calls/webhook.go`.
Checked against a real LiveKit v1.13.11 and a real WebRTC participant: joined, left, screen share started and stopped, and room finished all arrive, verify and land with LiveKit's times.

**Route**

`POST /api/livekit/webhook`, registered by `calls.RegisterWebhook` on the public `/api` group.
The signature is checked with `github.com/livekit/protocol/webhook`, which also checks the SHA-256 of the body.
Refused: 401. Signed but malformed: 400. Not ours: 200, ignored. Database trouble: 500, so LiveKit retries.
LiveKit is configured in both compose files and reaches the app directly: `http://teamup:8080` in production, `http://host.docker.internal:${TEAMUP_PORT:-8080}` in dev, because `start.sh` runs the app on the host.

**`GET /api/calls/:id/log`**

```json
{
  "events": [{"id": "", "type": "joined", "userId": "", "userName": "", "actorId": "", "actorName": "", "at": "RFC3339"}],
  "sessions": [{"userId": "", "userName": "", "joinedAt": "RFC3339", "leftAt": null}],
  "attendanceRecorded": true
}
```

`type` is `knocked`, `admitted`, `denied`, `joined`, `left`, `screen_share_started` or `screen_share_stopped`.
`attendanceRecorded: false` means show "no attendance data", not "nobody came" (decision 26).
Anybody who may not read it gets 404.

**`GET /api/calls/:id`** answers in full to whoever may read the log.
Anybody else gets `{"id","status","callType","canKnock"}`, plus `title` if they may join anyway (decision 28).
Full responses gain `scheduledStart`, `scheduledEnd`, `standingMeetingId`, `occurrenceDate` where they apply.

**Screen share**: tokens carry `canPublish` and no `canPublishSources`, so anybody in a call can share a screen, on every path that issues a token. A test pins it. Ticket 06 needs no server change for this.

**Join sound**: there is no server frame for somebody joining. Use LiveKit's own `ParticipantConnected` in the browser, which is immediate and does not depend on webhooks.

**Failure modes**

- Webhooks not arriving: ringing and missed calls are unaffected, because they read `answered_at` and `answered`, which the token sets. The log says no data. A scrum day reads `unknown`. The calendar falls back to the call's own times. Knocks go to the organizer and invitees by notification and frame instead of to whoever is connected. Sessions missed are not reconstructed.
- App down during a call: LiveKit retries, then drops. A join that was lost is recovered from the leave, which carries `joined_at`. A leave and a room finish both lost leave a session open, which attendance caps at the call's end or the end of that day.
- Replayed or retried webhook: every write has a natural key (`joined:<sid>`, `left:<sid>`, `screen_share_started:<track sid>`), so it lands once.
