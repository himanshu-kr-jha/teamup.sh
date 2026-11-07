# Somebody deactivated keeps access to calls through an open socket and old tokens

Status: ready-for-human
Severity: Medium

`DeactivateUser` (`internal/org/org.go:252`) revokes refresh tokens, which stops HTTP at once.
Nothing else in the call path looks at whether a person is active.

## Where

- `internal/calls/calls.go:1408`: `MayJoin` is true for a standing meeting with `JOIN users u ON u.org_id = c.org_id AND u.id = $3`, with no `deleted_at` or `is_active`.
- `internal/calls/calls.go:1390`: `IsParticipant` has no such check either.
- `internal/calls/calls.go:737`: `HandleCallAccept` relies on `MayJoin` and mints a token over the WebSocket.
- `internal/websocket/hub.go:948`: a socket is only closed when the access token that opened it expires, up to 15 minutes later (`cmd/teamup/main.go`, `AccessTokenTTL`). Deactivating closes nothing.
- `internal/calls/calls.go:142`: tokens use LiveKit's default lifetime of 6 hours, and they are not revoked.
- Nothing removes a deactivated person from a room they are connected to.

## Reproduction

Verified with a throwaway test: `UPDATE users SET is_active = false, deleted_at = NOW()` for a member, and `MayJoin` on today's occurrence returned `true, nil`.

End to end, as read from the code:
1. Somebody is let go at 10:00 with TeamUp open.
2. Their tab's socket lives until the access token expires. They send `{"type":"call.accept","payload":{"roomName":"standing-..."}}` and get a fresh 6 hour token for the morning scrum, or any call they were invited to.
3. They stay in, or rejoin, the room until about 16:00. Attendance records them.

## Suggested minimal fix

1. `MayJoin` and `IsMember` refuse anybody with `deleted_at IS NOT NULL OR NOT COALESCE(is_active, true)`.
2. On deactivation, close the person's sockets, as the hub can publish to every replica, and remove them from any room they have an open session in with `RoomService.RemoveParticipant`.
3. Set a shorter token lifetime, for example `SetValidFor(10 * time.Minute)`. LiveKit lets a connected participant carry on, and reconnection refreshes the token, so this needs checking against the real server first.

## Comments

2026-11-07, senior developer:
`MayJoin` and `IsMember` refuse anybody deactivated, which covers `HandleCallAccept` over a still-open socket.
`org.Service.OnDeactivated` tells `cmd/teamup/main.go`, which closes their sockets on every replica with `Hub.DisconnectUser` and removes them from rooms with `RemoveFromCalls`.
Token lifetime is untouched, as the user decided, so somebody can still connect with a token they had: the `participant_joined` webhook records that and removes them.
Verified: `TestSomebodyDeactivatedMayNotJoinAnything`, `TestDeactivatingSomebodyTakesThemOutOfEveryRoomTheyAreIn`, `TestSomebodyDeactivatedConnectingWithAnOldTokenIsPutOutAndLogged`, `TestDeactivatingAMemberTellsWhateverHoldsTheirConnections`, `TestDisconnectingSomebodyClosesAllTheirSocketsAndNobodyElses`, `TestDisconnectingSomebodyReachesTheirSocketOnAnotherProcess` (real Redis, which caught that the fan-out drops a publication with no message), and a real-browser test against real LiveKit under "ticket 26".
Not verified end to end: reconnecting to a room with an old token in a browser.
