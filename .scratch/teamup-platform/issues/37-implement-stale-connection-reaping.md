# 37 — Implement stale-connection reaping and pong tracking

**What to build:** `Hub.cleanupStaleConnections` is an empty loop body, and nothing tracks whether a client answers the 30-second ping. A half-open connection stays registered indefinitely: the user shows as permanently online and the connection's goroutines and buffer are never reclaimed. Track liveness and reap connections that stop answering.

**Blocked by:** None — can start immediately

**Status:** open

- [ ] Failing test: register a connection that never responds, advance the reaper, assert it is unregistered and the user goes offline
- [ ] Record a last-seen timestamp per connection, updated on every inbound frame
- [ ] Handle `conn.pong` (the type constant already exists) and refresh last-seen
- [ ] Implement `cleanupStaleConnections` to close and unregister connections past a deadline
- [ ] Give `Hub.Run` a stop channel so the reaper exits on shutdown

**Type:** bug

## Evidence

`internal/websocket/hub.go:298-306` — the reaper does nothing at all:

```go
func (h *Hub) cleanupStaleConnections() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, conn := range h.connections {
		_ = conn
		// Check if connection is still alive
	}
}
```

It is called every 30 seconds from `Hub.Run` (`hub.go:81-91`), so the timer fires and accomplishes nothing.

The ping side is half-built. `WriteLoop` sends a ping every 30 seconds (`hub.go:387-392`):

```go
case <-ticker.C:
	pingMsg := protocol.NewMessage(protocol.TypeConnPing, map[string]interface{}{})
	if err := c.writeJSON(c.ctx, pingMsg); err != nil {
		return
	}
```

`protocol.TypeConnPong` is declared at `pkg/protocol/messages.go:14`, but `Connection.handleMessage` (`hub.go:339-369`) has no case for it — a pong falls through to `default` and is logged as an unknown message type. No timestamp is recorded anywhere on `Connection` (`hub.go:34-46` has no last-seen field), so there is nothing for a reaper to compare against even once one is written.

### Failure mode

These are application-level ping frames over an established TCP connection, not TCP keepalives. When a client disappears without a FIN — laptop suspended, NAT table entry expiring, mobile switching networks — writes keep succeeding into the socket buffer for a long time before the OS gives up. Meanwhile:

- `h.connections`, `h.userConns` and `h.channelSubs` keep the entry (`hub.go:29-31`).
- `presenceService.SetOnline` was called at `hub.go:110` and the matching `SetOffline` at `hub.go:153` only runs from `Unregister`, which only runs when `WSHandler` returns at `hub.go:487-488`. The user is shown online to their whole organisation indefinitely.
- `ReadLoop` and `WriteLoop` goroutines plus a 256-entry buffer stay resident per stale connection.
- Every `broadcastPresence` (`hub.go:289-295`) and `BroadcastToOrg` (`hub.go:261-270`) walks these dead entries.

Separately, `Hub.Run` is `for { select { case <-ticker.C: ... } }` with a single case and no stop channel, so the goroutine cannot be shut down — relevant to ticket 38.

### Test shape

Register a connection whose `SendCh` is never drained and which never sends a frame. Call `cleanupStaleConnections` (or drive the reaper) after the liveness deadline. Assert the connection is gone from `h.connections`/`h.userConns` and that `SetOffline` fired. Today the connection is still registered and the user is still online, so the assertion fails.
