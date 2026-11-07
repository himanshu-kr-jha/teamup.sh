# Calendar, blocks, scheduled meetings and reminders

Status: resolved
Blocked by: 01

Implement decisions 1 to 5 and 16, with a new `internal/calendar` package.
The read merges blocks, meetings, past calls and standing meetings, for one person and a range.
Validate end after start, a sane maximum length, and people in the same organization. No double ringing when a scheduled meeting starts.

## Comments

### 2026-11-07 - Senior developer: built, and what the frontend needs

Migration `0020_calendar`: table `calendar_blocks`, column `calls.reminded_for`.
Code: `internal/calendar`. Reminders sweep every 30 seconds on every replica, and a single `UPDATE` claims them.

**How a meeting starts**: joining opens it, by `POST /api/calls/:id/token` with the meeting's id as the call id. It rings nobody (decision 22). Reconnect within a meeting's time reopens it (decision 23).

**Routes**

- `GET /api/calendar?userId=&from=&to=`, times RFC 3339, at most 92 days, `userId` defaulting to the caller: `{"items": [...]}`.
- With authority an item is `{"id", "kind", "source", "title", "startsAt", "endsAt", "callId", "standingMeetingId", "organizerId", "inviteeIds", "status"}`, leaving out what does not apply. Without authority it is only `{"kind", "startsAt", "endsAt"}`, so do not key a list on `id`. `kind` is `busy`, `focus` or `open`. `source` is `block`, `meeting`, `call` or `standing`.
- A standing slot has id `standing:<meetingId>/<date>` and no `callId`; join it through `POST /api/standing-meetings/:id/join`.
- `GET /api/calendar/availability?userIds=a,b&from=&to=`, at most 200 people: `{"people": [{"userId", "items": [{"kind", "startsAt", "endsAt"}]}]}`. Never titles. People not in the organization are left out.
- `POST /api/calendar/blocks` body `{"kind": "focus|open", "title", "startsAt", "endsAt"}`: 201 `{"block": {"id", "kind", "title", "startsAt", "endsAt"}}`.
- `PATCH /api/calendar/blocks/:id` any of those: 200 `{"block": {...}}`. `DELETE`: 200. Not the owner, admins included: 404.
- `POST /api/meetings` body `{"title", "startsAt", "endsAt", "inviteeIds": []}`: 201 `{"meeting": {"id", "title", "startsAt", "endsAt", "status", "organizerId", "inviteeIds"}, "conflicts": [{"userId", "kind": "focus|meeting"}]}`. The organizer's own conflicts are included. Somebody invited who is not active in the organization: 400.
- `PATCH /api/meetings/:id` any of `title`, `startsAt`, `endsAt`, `inviteeIds`: 200 with the same shape. `DELETE`: 200. Organizer or org admin only, others 403. Once it has opened: 409.

**Notifications** are ordinary rows, arriving over `notification.new`, with `data` of `{"callId", "title", "startsAt", "endsAt"}`.
Types: `meeting.invited`, `meeting.moved`, `meeting.cancelled`, `meeting.reminder`.

`GET /api/calls` now lists scheduled meetings to those on them and hides cancelled ones unless `status=cancelled` is asked for. `CallsPage` already has a style for `scheduled`, and its Join button works.
