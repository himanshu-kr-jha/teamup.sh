# 37 — Implement stale-connection reaping and pong tracking

**What to build:** `Hub.cleanupStaleConnections` is an empty loop body, and nothing tracks whether a client answers the 30-second ping. A half-open connection stays registered indefinitely: the user shows as permanently online and the connection's goroutines and buffer are never reclaimed. Track liveness and reap connections that stop answering.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Failing test: register a connection that never responds, advance the reaper, assert it is unregistered and the user goes offline
- [x] Record a last-seen timestamp per connection, updated on every inbound frame
- [x] Handle `conn.pong` (the type constant already exists) and refresh last-seen
- [x] Implement `cleanupStaleConnections` to close and unregister connections past a deadline
- [x] Give `Hub.Run` a stop channel so the reaper exits on shutdown (not yet called from `main.go` — see comments)

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

## Comments

**Root cause.** Two independent halves of the same failure. `cleanupStaleConnections`
ranged over the connections assigning each to `_`, so the 30-second timer did
nothing. Separately, the Redis presence hashes in `internal/presence` were
written with `HSET` and no expiry, so the only thing that ever removed one was
`Unregister` — which only runs on a clean shutdown. A process that is killed or
OOMs therefore left every user it served showing online for ever, and no other
writer would ever correct it.

**Tests added.**

`internal/websocket/reaper_test.go`:

- `TestAConnectionThatStoppedAnsweringIsReaped:79` — RED was
  `a connection that stopped answering is still registered`,
  `the user is still shown online to their organisation`,
  `the connection's goroutines were never stopped`.
- `TestTheHeartbeatRefreshesPresence:158` — RED was `the ping went out but the
  presence entry was not refreshed, so its TTL expires under somebody who is
  still here`.
- `TestAConnectionThatIsStillAnsweringIsLeftAlone:105` and
  `TestAnsweringThePingKeepsAConnectionAlive:126` both passed before the fix,
  because nothing reaped anything yet. The second was confirmed to bite by
  removing the `c.touch()` from `ReadLoop` after the fix and watching it fail
  with `a client that answered the ping was reaped anyway`.

`internal/presence/expiry_test.go`:

- `TestPresenceKeysCarryAnExpiry:30` — RED was `the presence key has no expiry
  (ttl -1ns): a process that dies leaves this user online for ever`.
- `TestPresenceDisappearsWhenNobodyRefreshesIt:47` — RED was `they are still
  shown as "online" long after the process serving them stopped`.
- `TestRefreshingReestablishesAnEntryThatAlreadyExpired:110` — RED was
  `a connection that is still here should have its entry re-established`.
- `TestRefreshingKeepsSomebodyWhoIsStillHere:79` passed before the TTL existed;
  confirmed to bite afterwards by stubbing out `Touch`'s `PExpire`.
- `TestGetAllOnlineOnlySeesItsOwnOrganisation:136` passed before and after. The
  `KEYS`→`SCAN` swap is a performance change with no behavioural difference, so
  this is regression cover for it, not a reproduction.

**Fix.** `Connection` gained an atomic `lastSeen`, set in `Register` and on every
inbound frame in `ReadLoop` — any frame at all counts as proof of life,
including one this server cannot parse. `conn.pong` gained a case in
`handleMessage` so a client answering correctly is not logged as an unknown
type. `cleanupStaleConnections` collects connections past `staleAfter` (95s,
three missed pings) under a read lock, releases it, then cancels, closes in the
background and unregisters each — so the user is marked offline and the
goroutines and buffer are reclaimed.

Presence keys now carry `presenceTTL` (90s, three heartbeats), set in the same
round trip as the fields via a pipeline so an entry can never exist without one.
`Service.Touch` extends it and is called from the connection heartbeat, which is
the ~30s ping loop that already existed. If the key has already gone — Redis
restarted, or was unreachable longer than the TTL — `Touch` re-establishes it,
because only a live connection is ever refreshed. `GetAllOnline` now walks the
keyspace with `SCAN` instead of `KEYS`, deduplicating as it goes (`SCAN` may
return the same key more than once).

**Two things found while fixing this, both fixed:**

- Calling `conn.finish()` inline in the reaper made the test take 5.01s.
  `WS.Close` waits for the peer to answer the close handshake, and a reaped peer
  by definition never does, so reaping N dead connections would have taken 5N
  seconds on the hub's single goroutine. The close now happens in the
  background; the cancel, which is what stops the loops, stays synchronous.
- `finish()` dereferenced `c.WS` unconditionally. Once the close path became
  reachable from broadcast goroutines it could panic there with no recover,
  killing the process. It now tolerates a nil socket.

**Left alone / follow-ups:**

- `Hub.Stop()` exists but `cmd/teamup/main.go` still calls `go wsHub.Run()`
  without ever calling it, so the reaper goroutine still outlives shutdown in
  practice. `main.go` was outside this change's scope; this belongs with
  ticket 38.
- `Touch` is reached through a new optional `PresenceRefresher` interface rather
  than being added to `PresenceServiceInterface`, because that interface has
  test doubles in `internal/chat` and elsewhere that were out of scope. The
  production wiring in `main.go` passes `*presence.Service`, which satisfies it,
  so the refresh does run. Folding it into the main interface would be tidier.
- Ticket 03 specifies that the server sends `presence.sync` after `conn.ack`.
  It still does not. Nothing was regressed here, but the handshake is not yet
  complete against that spec.
