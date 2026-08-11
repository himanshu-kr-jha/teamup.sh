# 41 — Authorize the typing indicator before broadcasting it

**What to build:** `HandleTyping` is the only chat WebSocket handler with no authorization at all. It passes a client-supplied `channelId` straight to `BroadcastToChannel`, so a user can make themselves appear to be typing in any channel on the instance, including private channels they are not a member of. Gate it like its siblings.

**Blocked by:** 33 (the handler is currently unreachable, so a test cannot fail until inbound payload decoding works)

**Status:** completed

- [x] Failing test: a non-member sends `chat.typing` for a private channel; assert members receive nothing
- [x] Check membership (or `AuthorizePost`) before broadcasting
- [x] Parse and use `conn.OrgID()` the way the other handlers do
- [x] Same check for any other handler that broadcasts on a client-supplied channel id

**Type:** bug

## Evidence

`internal/chat/chat.go:348-361` in full:

```go
func (s *Service) HandleTyping(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatTypingPayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(payload.ChannelID, protocol.NewMessage(protocol.TypeChatTyping, payload), conn.UserID())
	}
}
```

There is no `AuthorizePost`, no `isMember`, no org parse — `payload.ChannelID` goes directly into the broadcast. Compare the handlers on either side of it, all of which do gate:

- `HandleSendMessage` — `AuthorizePost(ctx, orgID, channelID, userID, false)` at `chat.go:218`
- `HandleEditMessage` — authorship check at `chat.go:278`
- `HandleDeleteMessage` — `chat.go:309` onward
- `HandleRead` — `chat.go:376`

`BroadcastToChannel` resolves recipients through `ChannelMemberIDs` (`internal/websocket/hub.go:212`), whose query is also org-unscoped (`chat.go:1048-1050`):

```sql
SELECT cm.user_id FROM channel_members cm WHERE cm.channel_id = $1
```

### Scenario to write the test from

Two users in one organisation. Create a **private** channel containing only user A. As user B (not a member), send over the socket:

```json
{"type":"chat.typing","payload":{"channelId":"<A's private channel>","isTyping":true}}
```

Expected: user A receives nothing. Actual (once ticket 33 lands): user A receives a `chat.typing` frame naming user B, confirming both that B knows the channel exists and that B can inject UI state into it. With a channel id from another organisation the same frame crosses the tenant boundary.

Nothing is returned to the sender — the payload is only `channelId` and `isTyping` (`pkg/protocol/messages.go:155-158`) — so this is one-way injection rather than a data read. It still leaks channel existence and is the one gap in an otherwise consistently gated set of handlers.

### Sequencing

Today this cannot be triggered: the `msg.Payload.([]byte)` assertion at `chat.go:350` always fails, so the handler returns before reaching the broadcast (ticket 33). Land 33 first, then this test will fail for the right reason. Do not close 33 without also closing this one — 33 turns a dead handler into a live unauthorized one.

## Comments

**Root cause.** As filed: `HandleTyping` handed a client-supplied `channelId`
straight to `BroadcastToChannel` with no membership check and no organization
parse. Fixing ticket 33 turned a dead handler into a live unauthorized one, so
this was closed in the same pass.

**Fix.** `internal/chat/chat.go:344` now parses `conn.OrgID()` and
`conn.UserID()` and calls `AuthorizePost(ctx, orgID, channelID, userID, false)`
— the same gate `HandleSendMessage` uses — refusing and logging otherwise.
`AuthorizePost` rather than a bare `isMember` on purpose: membership in a public
channel only materialises when somebody first posts, so `isMember` would have
hidden the typing indicator in public channels until you had posted in them. It
costs one query per typing frame; clients throttle the indicator, and the answer
to "may this person write here" is worth keeping in one place.

**Tests.** `internal/chat/ws_inbound_test.go:436` (non-member, private channel)
and `:455` (a channel belonging to another organization). Both drive real frames
over a real socket. `:407` covers the positive case, so the gate cannot be
satisfied by refusing everybody.

**RED evidence.**

```
--- FAIL: TestATypingFrameFromANonMemberReachesNobody
    member received chat.typing, which should never have been sent:
    {"channelId":"706c2e96-aec1-4bdb-9df6-981f4273abd9","isTyping":true}
--- FAIL: TestATypingFrameCannotCrossOrganizations
    their-member received chat.typing, which should never have been sent:
    {"channelId":"83b38ba7-64d1-44d4-ae3e-09c53896bf71","isTyping":true}
```

**Audit of the other handlers.** `HandleTyping` was the only handler that
broadcast on a client-supplied channel id. `HandleSendMessage` already called
`AuthorizePost`; `HandleEditMessage` and `HandleDeleteMessage` resolve the
channel from the message id server-side after an authorship or admin check;
`call.accept` and `call.decline` broadcast only back to the sender.

**Left alone.** `HandleRead` (`chat.go:359`) takes a client-supplied channel id
but broadcasts nothing, and `updateLastRead` is scoped by `(channel_id, user_id)`
in `channel_members`, so a non-member's frame matches no row and does nothing.
The org-unscoped `ChannelMemberIDs` query noted in the evidence
(`chat.go:1043`) is untouched: it is a delivery roster reached only through
handlers that have already authorized the channel, and narrowing it is a
separate change.
