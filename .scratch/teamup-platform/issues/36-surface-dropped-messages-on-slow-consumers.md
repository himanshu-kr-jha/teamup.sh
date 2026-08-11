# 36 — Stop silently discarding messages when a client's send buffer is full

**What to build:** `Connection.Enqueue` drops the message on the floor when the 256-slot buffer is full, with no log, no metric, and no signal to the client. A user on a slow link silently loses live messages and has no way to know their view is stale. Make the drop observable and give the client a way to recover.

**Blocked by:** None — can start immediately

**Status:** open

- [ ] Failing test: fill a connection's buffer past 256, assert the drop is counted/logged rather than invisible
- [ ] Increment a Prometheus counter and log at warn on drop
- [ ] Decide and implement a policy: close the socket so the client reconnects and refetches, or send a `resync` hint
- [ ] Document the chosen behaviour alongside the 256 buffer in the WebSocket decision

**Type:** bug

## Evidence

`internal/websocket/hub.go:56-62`:

```go
func (c *Connection) Enqueue(msg *protocol.Message) {
	select {
	case c.SendCh <- msg:
	default:
		// Buffer full - client is slow
	}
}
```

The buffer itself matches the unified-WebSocket decision — `SendCh: make(chan *protocol.Message, 256)` at `hub.go:457`. What is missing is everything after the drop. The `default` branch is empty: no counter, no `c.logger` call, no disconnect.

Consequences for a client whose `WriteLoop` is behind (mobile on a weak connection, a backgrounded tab, a client that stopped reading):

- Chat messages routed through `BroadcastToChannel` (`hub.go:239`) vanish for that user. They are still in Postgres, so a manual reload recovers them — but nothing tells the client to reload, and the offline cache described in the PWA decision will happily serve the gap as if complete.
- `call.invited` frames are dropped the same way (`internal/calls/calls.go:307`), so an incoming call simply never rings.
- Presence updates (`hub.go:292`) desynchronise with no correction, because `broadcastPresence` only fires on transitions and there is no periodic resync.

`internal/observability/observability.go` defines WebSocket metrics, and none of them count drops, so an operator cannot even tell it is happening. The only symptom is users reporting missing messages.

### Test shape

Construct a `Connection` with a `SendCh` that nothing drains, call `Enqueue` 300 times, and assert the drop is observable — a counter above zero, or a log entry. Today the 44 overflow messages disappear with no trace of any kind, so any such assertion fails.
