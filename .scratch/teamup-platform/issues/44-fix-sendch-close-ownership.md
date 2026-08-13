# 44 — Stop closing SendCh from the reader while producers can still enqueue

**What to build:** `ReadLoop`'s deferred cleanup closes `Connection.SendCh`, but that channel has many concurrent producers — every broadcast path calls `Enqueue` on connection pointers it obtained earlier. A send on a closed channel panics, and the closest producers run in goroutines with no recover, so the process dies. Give the channel a single owner and stop the writer via context instead of closing from the reader.

**Blocked by:** None — can start immediately (but see sequencing)

**Status:** completed

- [x] Failing test under `-race`: broadcast to a connection concurrently with its `ReadLoop` terminating, looped until it trips
- [x] Stop closing `SendCh` from `ReadLoop`; let `WriteLoop` exit on `ctx.Done()`
- [x] Have `Enqueue` respect `c.ctx` so it becomes a no-op once the connection is finished
- [x] Confirm no path can `Enqueue` after cleanup

**Type:** bug

## Evidence

`internal/websocket/hub.go:308-315` — the reader closes the channel:

```go
func (c *Connection) ReadLoop() {
	defer func() {
		c.CloseOnce.Do(func() {
			c.cancel()
			close(c.SendCh)          // :312
			c.WS.Close(websocket.StatusNormalClosure, "")
		})
	}()
```

`internal/websocket/hub.go:56-62` — the producers:

```go
func (c *Connection) Enqueue(msg *protocol.Message) {
	select {
	case c.SendCh <- msg:            // :58  panics if SendCh is closed
	default:
	}
}
```

A `select` with a `default` does **not** protect against a closed channel. `send on closed channel` is a panic regardless of the `default` arm; `default` only covers a full buffer.

The producers hold `*Connection` pointers taken from the hub maps and call `Enqueue` after the lock is released, so there is no ordering between "connection cleaned up" and "somebody enqueues to it":

- `BroadcastToChannel` — snapshots `conns` under `RLock` at `hub.go:227-235`, releases at `:236`, then calls `conn.Enqueue(msg)` at `:239`
- `BroadcastToUser` — `hub.go:252`
- `BroadcastToOrg` — `hub.go:268`
- `broadcastPresence` — `hub.go:292`

Unrecovered callers, where a panic kills the whole process rather than one request:

- `go h.service.Ring(...)` — `internal/calls/routes.go:136`, reaching `Enqueue` via `calls.go:307` and `:312`
- `go func(){ ... DeliverPendingInvitations(...) }()` — `internal/websocket/hub.go:117-122`
- `go conn.ReadLoop()` — `hub.go:484`; once ticket 33 lands, the chat handlers enqueue from inside `ReadLoop` (`chat.go:249`)

### Sequencing and current reachability

Today the `ReadLoop` producer path is masked by ticket 33: every inbound handler returns before enqueueing, so `ReadLoop` itself never produces. The remaining windows (`Ring`, `DeliverPendingInvitations`, presence broadcasts) are narrow, and in practice the same stress reproduced ticket 34's unlocked map iteration first — that fatal error fires on the identical interleaving and kills the process before this panic gets a chance.

Fix 34 first. Then this becomes the next failure on the same race, and once 33 lands, `ReadLoop` becomes a producer too and widens the window considerably. All three want fixing in one pass over the hub.

### Test shape

Build a hub with one registered connection whose `WriteLoop` is running. In a loop: start a goroutine calling `BroadcastToUser` repeatedly while another triggers the connection's `ReadLoop` cleanup. Run under `-race` with a few thousand iterations. The correct implementation never panics; the current one panics with `send on closed channel`.

## Comments

**Root cause.** As filed: the reader closed a channel with many concurrent
producers, and `select` with a `default` arm does not protect a send against a
closed channel.

**Fix.** `SendCh` now has no closer at all. `ReadLoop`'s cleanup moved into
`Connection.finish` (`internal/websocket/hub.go:348`), which cancels the context
and closes the socket; cancelling is what stops `WriteLoop`, which already
returned on `<-c.ctx.Done()`. `Enqueue` (`hub.go:64`) checks the context first
and gives up. The channel is garbage collected with the connection.
`WriteLoop`'s `msg, ok := <-c.SendCh` receive lost its now-dead `!ok` arm.

**Test.** `internal/websocket/hub_test.go:415` —
`TestBroadcastingToAConnectionThatIsClosing`. It runs the real `ReadLoop` behind
a real WebSocket (`newSockets`, `hub_test.go:365`) and closes the client end,
which is how a session ends in practice, while a second goroutine loops
`BroadcastToUser`. 50 rounds.

**RED evidence.** Both the data race and the crash the ticket predicted:

```
WARNING: DATA RACE
Write at 0x00c00051a080 by goroutine 31:
  runtime.closechan()
  websocket.(*Connection).ReadLoop.func1.1()   internal/websocket/hub.go:329
Previous read at 0x00c00051a080 by goroutine 32:
  runtime.chansend()
  websocket.(*Connection).Enqueue()            internal/websocket/hub.go:58
...
panic: send on closed channel

goroutine 66 [running]:
websocket.(*Connection).Enqueue(...)          internal/websocket/hub.go:58
websocket.(*Hub).BroadcastToUser(...)         internal/websocket/hub.go:262
```

Green after the fix, including `-count=3`. The same race also showed up
unprompted in the ticket 33 socket harness before this was fixed, which is
independent confirmation that the window is not theoretical.

**On "no path can Enqueue after cleanup".** A producer can still pass the
context check microseconds before `finish` cancels, and its message then lands in
a buffer nobody will read. That is harmless — the message is dropped, exactly as
it is for a slow consumer, and the connection is collected. Making it impossible
would need a per-connection lock taken on every broadcast, which is a real cost
for no benefit. The guarantee this fix makes is that a send after cleanup cannot
panic, not that it cannot happen.
