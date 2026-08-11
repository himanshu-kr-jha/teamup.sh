# 33 — Decode inbound WebSocket payloads instead of type-asserting them to []byte

**What to build:** Every client→server WebSocket message is silently discarded because all ten inbound handlers type-assert `msg.Payload.([]byte)` against a field that `encoding/json` always populates with `map[string]interface{}`. Make the envelope decode carry the raw payload bytes (e.g. `Payload json.RawMessage`) or re-marshal in the handlers, so inbound frames are actually processed.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Failing test: a real frame goes in one end of the hub and the handler observes the decoded payload
- [x] Fix the envelope so inbound payloads decode (`pkg/protocol/messages.go:55`)
- [x] Update all ten handlers that assert `.([]byte)`
- [x] Integration test: two connected clients, one sends `chat.send`, the other receives `chat.message` AND the row is in `messages`
- [x] Cover `chat.edit`, `chat.delete`, `chat.typing`, `chat.read`, `presence.update`, `call.invite`, `call.accept`, `call.decline`, `call.signaling` the same way

**Type:** bug

## Evidence

`pkg/protocol/messages.go:55` declares the envelope field as an empty interface:

```go
Payload   interface{} `json:"payload"`
```

`internal/websocket/hub.go:328-334` decodes an inbound frame straight into that struct:

```go
var msg protocol.Message
if err := json.Unmarshal(data, &msg); err != nil { ... }
c.handleMessage(&msg)
```

`encoding/json` unmarshals into an `interface{}` field as `map[string]interface{}` — never `[]byte`. Every handler then does this and returns:

```go
payloadBytes, ok := msg.Payload.([]byte)
if !ok {
    return
}
```

All ten occurrences: `internal/chat/chat.go:190` (send), `:261` (edit), `:309` (delete), `:350` (typing), `:365` (read); `internal/calls/calls.go:514` (invite), `:560` (accept), `:584` (decline), `:602` (signaling); `internal/presence/presence.go:261` (presence update).

So `ok` is always false and **100% of client→server WebSocket traffic is dropped without a log line.** The socket works only in the server→client direction, which is why presence broadcasts (server-initiated from `Hub.Register`, `hub.go:111`) appear to work and make the feature look alive.

### Reproduction (verified against a live server)

Server booted on :8099 against Postgres+Redis+MinIO+LiveKit, org auto-set-up, two users (`admin`, `mallory`) both members of channel `41aa595f-9e8b-47c1-b5bf-d1c2244403da`.

1. Admin opens `ws://localhost:8099/ws` with `Authorization: Bearer <at>`; mallory opens a second socket.
2. Mallory sends exactly this frame:
   ```json
   {"type":"chat.send","payload":{"channelId":"<channelId>","content":"hello over WEBSOCKET"}}
   ```
3. Mallory then sends `{"type":"chat.typing","payload":{"channelId":"<channelId>","isTyping":true}}`.

Observed:
- Admin receives only `presence.changed` frames — never `chat.message`, never `chat.typing`.
- Mallory never receives the `chat.sent` acknowledgement that `chat.go:249` is supposed to enqueue.
- `SELECT content FROM messages` contains only the message posted over REST. The WebSocket message was never persisted.
- Nothing is logged: the handler returns before any log statement.

Contrast: `POST /api/channels/:id/messages` persists and returns 201 correctly, so the failure is specific to the WebSocket path.

### Why the suite is green

No test anywhere calls an inbound handler. `grep -rn "HandleSendMessage\|HandleTyping\|HandleCallInvite\|HandleEditMessage\|HandlePresenceUpdate\|HandleRead\|HandleDeleteMessage" --include="*_test.go" internal/` returns only a stub method declaration at `internal/websocket/hub_test.go:21`. The hub tests call `BroadcastToChannel` directly (`hub_test.go:177`, `:222`), bypassing `Connection.handleMessage` entirely. Any regression test must drive a real frame through `handleMessage`, not call the service method with a hand-built payload.

### Follow-on

Fixing this makes two currently-masked bugs reachable — see tickets 41 (`HandleTyping` has no authorization) and 44 (send on closed channel from `ReadLoop`). Fix those in the same pass or immediately after.

## Comments

**Root cause.** One struct served both directions of the socket and only one of
them worked. `protocol.Message.Payload` was `interface{}`, which `encoding/json`
fills with `map[string]interface{}` on the way in — never `[]byte`. All ten
inbound handlers asserted it to `[]byte` and returned when that failed, so every
client-to-server frame was discarded before any log statement. Outbound was
unaffected, which is why the socket looked alive.

**Fix.** `Payload` is now `json.RawMessage` (`pkg/protocol/messages.go:65`), the
one representation that works both ways: `encoding/json` fills it with the raw
bytes coming in, and emits them verbatim going out. `NewMessage` marshals its
argument once at construction via `MarshalPayload`. **The wire format is
unchanged** — a payload object, exactly as before — so the frontend needed no
change; `web/src/features/chat/ChatPage.tsx:207` still reads `msg.payload` as an
object. All ten handlers now do `json.Unmarshal(msg.Payload, &payload)`.

**Tests.** The regression tests drive real frames over a real WebSocket into the
real `WSHandler` / `ReadLoop` / hub against a real database; only JWT validation
is stubbed. Calling a handler with a hand-built `protocol.Message` would have
passed throughout, since that choice of concrete payload type is exactly what
the bug was about.

- `internal/chat/ws_inbound_test.go:226` — two clients, one sends `chat.send`,
  the other receives `chat.message`, and the row is in `messages`
- `internal/chat/ws_inbound_test.go:289`, `:331`, `:366`, `:407` — `chat.edit`,
  `chat.delete`, `chat.read`, `chat.typing`, each asserted on its observable
  effect (persisted content, `deleted_at`, the read marker, delivery)
- `internal/chat/ws_inbound_test.go:263` — a non-member's `chat.send` is still
  refused now that the frame actually arrives
- `internal/presence/ws_inbound_test.go:55` — `presence.update` reaches Redis
- `internal/calls/ws_inbound_test.go:118`, `:162`, `:180` — `call.invite`
  persists the call and rings the invitee; `call.accept` and `call.decline` use
  the room the frame names
- `pkg/protocol/messages_test.go:18`, `:53`, `:85`, `:108` — the envelope
  contract: inbound decode, unchanged outbound wire format, the nested
  `call.signaling` payload, and a full round trip

**RED evidence.** Before the fix, `pkg/protocol/messages_test.go` would not even
compile — `cannot use msg.Payload (variable of type interface{}) as []byte value
in argument to json.Unmarshal` — the contract could not be expressed against the
old type. The behaviour tests failed with `sender never received chat.sent:
context deadline exceeded`, `the presence.update frame was dropped: nothing was
stored`, and `the call.invite frame was dropped: 0 calls exist`.

**Left alone.** `call.signaling` relays nothing (`internal/calls/calls.go:600`
ends in `_ = payload`), so it has no observable effect to assert; its decoding is
pinned at the envelope level instead (`pkg/protocol/messages_test.go:85`).
`MarshalPayload` turns an unencodable outbound payload into `null` rather than
returning an error, because `NewMessage` has many callers and no error channel;
no declared payload type in the package can fail to encode.
