# 10 - call.accept hands a LiveKit token for any room name to any user

Status: resolved
Type: bug

## What is wrong

The `call.accept` WebSocket frame signs a LiveKit join token for whatever `roomName` the client sends. It never looks up the call, never checks the organisation, never checks that the caller was invited or is in the channel, and never checks the call has not ended. Any signed-in user, in any organisation on the instance, can join (and publish audio/video into) any live call whose room name they know or guess. Room names are `"call-" + 8 hex chars` (32 bits), see `internal/calls/calls.go:144`.

The README "Known gaps" entry for this is still true. The REST path `POST /calls/:id/token` does the checks (`internal/calls/routes.go` GetCallToken, uses `MayJoin`); the socket path skips all of them.

## Where

- `internal/calls/calls.go:713-731` `HandleCallAccept` - `GenerateToken(ctx, payload.RoomName, ...)` with no authorisation.
- Reachable: `internal/websocket/hub.go:815` dispatches `call.accept` for every authenticated socket.
- Contrast `internal/calls/calls.go:1333` `MayJoin` (already exists) and `GetCallToken` in `internal/calls/routes.go`.

## How to reproduce

Test in `internal/calls` next to `ws_inbound_test.go`:
1. Seed org A with a call created through `CreateCall` (note `call.RoomName`).
2. Seed org B with a user, dial a socket as that user.
3. Send `{"type":"call.accept","payload":{"roomName":"<org A room name>"}}`.
4. Today the socket receives `call.invited` carrying a non-empty `Token` for that room. It should receive nothing (or a refusal), and no token.
Same with a user in org A who was not invited to a 1:1 call.

## Simple fix

In `HandleCallAccept`, look the call up by `room_name` and the socket's `OrgID` (new small query, or reuse `callColumns`), return silently if not found or `status = 'ended'`, then call `MayJoin(ctx, orgID, call.ID, userID)` and return if false. Only then generate the token. Optionally also fill `CallID` in the payload.

## Answer

`HandleCallAccept` now finds the call by room name in the socket's own organization, skips ended calls, and asks `MayJoin` before signing a token, the same rules as `POST /calls/:id/token`.
A refusal is silent, and the frame now also carries `callId`.
Test: `TestACallAcceptFrameTokenIsOnlyForSomebodyEntitledToJoin` in `internal/calls/ws_inbound_test.go:327`, which replaces `TestACallAcceptFrameUsesTheRoomItNames`, because that test pinned the defect by accepting a room that did not exist.
Removed the entries in `README.md` and `docs/ARCHITECTURE.md` that listed this as an open gap.
