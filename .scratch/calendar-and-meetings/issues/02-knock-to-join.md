# Knock, admit and deny for private calls

Status: resolved
Blocked by: 01

Implement decisions 7, 8 and 9 with the routes in the spec.
Replace the bare 403 with something the client can act on. WebSocket frames for the ping, for the result to the knocker, and to clear the prompt on every other member once somebody answers.
Requests end when the call ends. Everything is written to `call_events`.

## Comments

### 2026-11-07 - Senior developer: built, and what the frontend needs

Migration `0018_call_knock_requests`. Code: `internal/calls/knock.go`.

**Routes**

- `POST /api/calls/:id/token` for somebody who must knock: 403 `{"error": "...", "code": "knock_required", "canKnock": true}`. Ended or cancelled: 409. Another organization: 404.
- `POST /api/calls/:id/knock`: 200 `{"request": {"id", "userId", "userName", "status": "pending", "createdAt"}}`. A repeat while pending returns the same request. After a denial, within a minute: 429 with `Retry-After` and `{"error", "retryAfterSeconds"}`. Call over, or the caller may already join: 409.
- `GET /api/calls/:id/requests`, members only: `{"requests": [{"id", "userId", "userName", "status", "createdAt"}]}`, newest first, status `pending`, `admitted`, `denied` or `ended`.
- `POST /api/calls/:id/requests/:rid/admit` and `/deny`, members only: 200. Already answered or call over: 409. Not a member, admins included: 403.

**WebSocket frames** (constants and payload types in `pkg/protocol/messages.go`)

```json
{"type": "call.knock", "payload": {"callId": "", "requestId": "", "userId": "", "userName": "", "connected": true}}
{"type": "call.knock.result", "payload": {"callId": "", "requestId": "", "status": "admitted"}}
{"type": "call.knock.resolved", "payload": {"callId": "", "requestId": "", "status": "admitted", "answeredBy": ""}}
```

- `call.knock` goes to every member connected to the call, with `connected: true`. With nobody connected it goes to the organizer and invitees with `connected: false`, alongside a `call.knock` notification whose data has `callId` and `requestId`.
- `call.knock.result` goes to the knocker. `status` is `admitted`, `denied` or `ended`. On `admitted`, call `POST /calls/:id/token`, which now succeeds and goes on succeeding on reconnect.
- `call.knock.resolved` goes to the organizer, invitees and anybody connected, except whoever answered and whoever knocked, to clear the prompt.

A new knock frame type also has to be added to `web/src/shared/types/protocol.ts` by the frontend ticket.

**Other grants checked**: `HandleCallAccept` follows `MayJoin` and now only answers for a live call. `Ring` and `DeliverPendingInvitations` only reach people already on the call and in the organization. `HandleCallInvite` resolves everybody against the organization first.
