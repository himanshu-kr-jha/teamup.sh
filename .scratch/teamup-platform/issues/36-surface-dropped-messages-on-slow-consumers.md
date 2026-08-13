# 36 — Stop silently discarding messages when a client's send buffer is full

**What to build:** `Connection.Enqueue` drops the message on the floor when the 256-slot buffer is full, with no log, no metric, and no signal to the client. A user on a slow link silently loses live messages and has no way to know their view is stale. Make the drop observable and give the client a way to recover.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Failing test: fill a connection's buffer past 256, assert the drop is counted/logged rather than invisible
- [x] Increment a Prometheus counter and log at warn on drop
- [x] Decide and implement a policy: close the socket so the client reconnects and refetches, or send a `resync` hint
- [x] Document the chosen behaviour alongside the 256 buffer in the WebSocket decision

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

## Comments

**Root cause.** `Connection.Enqueue`'s `default:` arm was empty. A full send
buffer discarded the message with no counter, no log and no effect on the
connection, so a client that stopped reading diverged from the server silently
and permanently — the messages are still in Postgres, but nothing ever told the
client to go back and look.

**Tests added.** `internal/websocket/backpressure_test.go`:

- `TestDroppingAMessageToASlowClientIsReported:52` — RED was
  `dropped messages counted: got 0 want 5` and
  `the drop was not logged against the connection: ""`.
- `TestAClientThatNeverCatchesUpIsDisconnected:79` — RED was
  `a connection that never keeps up was left open, diverging silently`.
- `TestCatchingUpForgivesEarlierDrops:103` — passed before the fix as well.
  Nothing closed anything then, so it is not a reproduction; it is there to stop
  the close policy over-reaching onto clients that recover from a burst.

**Fix.** `Enqueue` now resets a `consecutiveDrops` counter on every message that
lands and calls `dropMessage` when one does not. `dropMessage` increments
`teamup_ws_messages_dropped_total{type}` (new, declared in
`internal/websocket/metrics.go`), logs at warn with the connection, user and
message type, and closes the connection once `dropsBeforeClose` messages have
been lost *in a row* — 32, justified in a comment on the constant. Consecutive
rather than cumulative is the point: it means the writer has moved nothing at
all, on top of a 256-slot buffer, which is a wedged client rather than a slow
one. The client already reconnects and refetches on an unclean close.

The metric is declared in `internal/websocket` rather than added to
`internal/observability` alongside the other `teamup_ws_*` metrics, because that
package was outside this change's scope. It registers with the default registry
either way, so `/metrics` serves it. Worth folding in with the others later.

**Left alone.** No `resync` hint was added — closing the socket reuses the
reconnect path the client already has, and a new message type would need client
work to be worth anything. `internal/calls` and `internal/chat` were not
touched; they benefit automatically, since they all deliver through `Enqueue`.
