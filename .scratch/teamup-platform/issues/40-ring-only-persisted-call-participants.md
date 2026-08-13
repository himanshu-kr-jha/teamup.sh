# 40 — Ring only the invitees CreateCall actually accepted

**What to build:** `CreateCall` filters invitees by organisation when it writes `call_participants`, then throws that result away and rings the raw client-supplied list instead. The single org check in the path is discarded, so a caller can ring a user in another organisation, mint them a LiveKit grant, push attacker-controlled text to their devices, and read back whether they are online. Derive the ring list from what was persisted.

**Blocked by:** None — can start immediately

**Status:** open

- [ ] Failing test: `POST /api/calls` with an invitee from another org — assert nothing is pushed, no `call.invited` is sent, and no `call.reach` is returned
- [ ] Return the accepted participant list from `CreateCall` and ring that
- [ ] Add an `org_id` predicate to `DisplayName` (`calls.go:1035`)
- [ ] Add an `org_id` predicate to `getSubscriptions` (`notifications.go:232`)
- [ ] Org-scope `Hub.IsConnected` so reachability cannot be probed across organisations

**Type:** bug

## Evidence

`internal/calls/routes.go` — `CreateCall`:

- `routes.go:82` takes `invitees := req.Invitees` verbatim from the request body. When `channelId` is omitted, the channel-membership gate at `routes.go:95-103` is skipped entirely.
- `internal/calls/calls.go:198-204` does the right thing at the database: the participant insert filters `u.org_id = $4`, so **no `call_participants` row is written** for an out-of-org user.
- `routes.go:128-133` then rebuilds `inviteeIDs` from the same unfiltered `req.Invitees` slice, and `routes.go:136` fires `go h.service.Ring(ringCtx, call, user.UserID, name, inviteeIDs)`.

So the filter runs, proves the user does not belong, and its result is never consulted.

`Ring` (`calls.go:281-319`) then, for each invitee:

1. `calls.go:291` → `DisplayName`, whose query has no org predicate (`calls.go:1035-1037`):
   ```sql
   SELECT COALESCE(name, ''), COALESCE(email, '') FROM users WHERE id = $1
   ```
2. `calls.go:284` → `ringDevices` (`calls.go:812-832`) → `notifier.PushToUsers` → `getSubscriptions`, also org-unscoped (`notifications.go:232-236`):
   ```sql
   SELECT id, org_id, user_id, endpoint, p256dh, auth, ...
   FROM push_subscriptions WHERE user_id = $1
   ```
   The victim's devices get a Web Push whose `Title` is the caller's name and whose `RequireInteraction: true` holds the lock screen (`calls.go:826-828`).
3. `calls.go:307` sends them `call.invited` carrying a **valid LiveKit join grant** for a room in the caller's organisation, minted at `calls.go:291`.
4. `calls.go:312-316` sends `call.reach` **back to the caller** with `"reachable": s.isReachable(invitee.String())`. That resolves to `Hub.IsConnected` (`internal/websocket/hub.go:163-167`), a bare `len(h.userConns[userID]) > 0` with no org check — a pollable oracle for whether an arbitrary user id has a session open.

### Scenario to write the test from

Seed two organisations, A and B, each with one user. Authenticate as A's user and send:

```
POST /api/calls
{"callType":"video","invitees":["<uuid of B's user>"]}
```

Assert: no push subscription lookup for B's user, no `call.invited` frame delivered to them, and the `call.reach` frame is either absent or reports nothing about them. Today the call row is created, `call_participants` correctly contains no row for B's user, and yet all four effects above still happen.

### Severity note for this deployment model

v1 is single-tenant, so a second organisation is not expected in a production database — which caps the practical blast radius today. It is still worth fixing now for two reasons: the codebase already treats `org_id` as an enforced invariant everywhere else (see the sweep below), and the same defect has an intra-org edge — the rung list is not the persisted list, so invitees rejected by the insert for any future reason still get a room grant.

### Related, verified safe

A full sweep of every SQL statement in `chat.go`, `tasks.go`, `calls.go`, `org.go`, `files.go`, `notifications.go`, `presence.go`, `auth.go` and `wizard/*.go` found no other reachable cross-org read and no case where `org_id` is taken from the request rather than the JWT. Handlers source it from `auth.GetUserFromContext(c).OrgID` (set from verified claims at `auth.go:401`), WS handlers from `conn.OrgID()` (`hub.go:52`, set at `hub.go:455`), and no payload struct in `pkg/protocol/messages.go` carries an `orgId` field.

Worth hardening opportunistically, though none is currently exploitable — cross-org `channel_members`/`team_members` rows cannot be created, which is what makes them safe: `tasks.go:1046`, `tasks.go:1120`, `chat.go:1028`, `org.go:584` are existence checks that omit `org_id`.
