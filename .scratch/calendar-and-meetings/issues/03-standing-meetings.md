# Standing meetings and daily attendance

Status: resolved
Blocked by: 01

Implement decisions 12 to 15.
An occurrence is a `calls` row, unique per standing meeting and date, created by the first join, safe against two people joining at once.
The occurrence ends when its room empties, and joining later the same day reopens the same one, so a day has one record.

## Comments

### 2026-11-07 - Senior developer: built, and what the frontend needs

Migration `0019_standing_meetings`: table `standing_meetings`, and on `calls` the columns `scheduled_start`, `scheduled_end`, `standing_meeting_id`, `occurrence_date`, with a unique index on the last pair that makes simultaneous joins one row.
Code: `internal/calls/standing.go`.

**Routes**

- `GET /api/standing-meetings`: `{"standingMeetings": [{"id", "name", "timeOfDay": "HH:MM", "timeZone", "weekdays": [1, 2], "durationMinutes", "createdBy", "createdAt"}]}`, archived excluded, weekdays ISO with Monday as 1.
- `POST /api/standing-meetings`, admin or owner: body `{"name", "timeOfDay", "timeZone", "weekdays", "durationMinutes"}`, the last optional and defaulting to 30. 201 `{"standingMeeting": {...}}`. Invalid: 400. Others: 403.
- `PATCH /api/standing-meetings/:id`, admin or owner: any of those fields. 200 `{"standingMeeting": {...}}`. A day already held keeps its start.
- `DELETE /api/standing-meetings/:id`, admin or owner: archives. 200.
- `POST /api/standing-meetings/:id/join`, anybody in the organization: 200 `{"callId", "roomName", "token", "date": "YYYY-MM-DD"}`. Archived or another organization: 404. It reopens the day's call if the room emptied.
- `GET /api/standing-meetings/:id/attendance?date=` or `?from=&to=`, optional `teamId`, at most 92 days, default today in the meeting's zone:

```json
{"days": [{
  "date": "YYYY-MM-DD", "callId": null, "scheduledStart": "RFC3339", "attendanceRecorded": true,
  "people": [{"userId": "", "name": "", "status": "present", "firstJoinAt": null, "lastLeaveAt": null, "minutesPresent": 0, "minutesLate": 0}]
}]}
```

`status` is `present`, `absent` or `unknown`. `unknown` and `attendanceRecorded: false` go together, and mean webhooks may be down (decision 26).
`lastLeaveAt` is null while somebody is still connected.
`callId` is null on a day nobody joined. Past run days with nobody joining are listed once due; future days and days it does not run are left out unless somebody came.
Scope follows decision 15. A team outside the caller's own: 404. An employee's `teamId` is ignored.
