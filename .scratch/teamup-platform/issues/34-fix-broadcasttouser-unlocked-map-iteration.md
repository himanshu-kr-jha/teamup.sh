# 34 — Hold the hub lock while iterating a user's connections in BroadcastToUser

**What to build:** `Hub.BroadcastToUser` releases the read lock and then ranges over the inner connection map. A concurrent `Register`/`Unregister` writing that same map triggers Go's `fatal error: concurrent map iteration and map write`, which is **not recoverable** — `gin.Recovery` cannot catch it and the whole process dies. Snapshot the connections under the lock before iterating, as `BroadcastToChannel` already does.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Failing test under `-race`: concurrent `Register`/`Unregister` for one user while `BroadcastToUser` runs in a loop
- [x] Snapshot into a slice while holding `h.mu.RLock()`, then iterate the slice
- [x] Audit the other broadcast methods for the same shape
- [x] Keep `Enqueue` outside the lock so a slow consumer cannot stall the hub

**Type:** bug

## Evidence

`internal/websocket/hub.go:243-255`:

```go
func (h *Hub) BroadcastToUser(userID string, msg *protocol.Message) {
	h.mu.RLock()
	userConns, ok := h.userConns[userID]   // :245
	h.mu.RUnlock()                          // :246  <-- lock released

	if !ok {
		return
	}

	for _, conn := range userConns {        // :252  <-- map iterated with NO lock held
		conn.Enqueue(msg)
	}
}
```

`userConns` is the inner `map[string]*Connection`, not a copy. Writers mutate that exact map:
- `Hub.Register` — `h.userConns[conn.UserID()][conn.ID] = conn` (`hub.go:106`)
- `Hub.Unregister` — `delete(userConns, conn.ID)` (`hub.go:136`)

Both take `h.mu.Lock()`, but that does not help a reader who is iterating without holding anything.

Compare `BroadcastToChannel` (`hub.go:227-240`), which does it correctly: it builds a `conns` slice while holding `RLock` and iterates the slice after `RUnlock`. `BroadcastToOrg` (`hub.go:257-271`) and `broadcastPresence` (`hub.go:279-296`) hold `RLock` via `defer` for the whole body. `BroadcastToUser` is the only offender.

### Reproduction — crashed a live server (verified)

Server on :8099 with the full dependency stack. Two users in one org. Driver: 6 coroutines repeatedly opening a WebSocket as `mallory`, sleeping 20ms and closing abruptly; 4 coroutines concurrently issuing `POST /api/calls` with `{"callType":"audio","invitees":["<mallory-uuid>"]}` every 10ms. Ran for 30 seconds.

The process died. From the server log:

```
fatal error: concurrent map iteration and map write

goroutine 1981 [running]:
internal/runtime/maps.(*Iter).Next(...)
github.com/teamup/teamup/internal/websocket.(*Hub).BroadcastToUser(...)
	/…/internal/websocket/hub.go:252 +0x145
github.com/teamup/teamup/internal/calls.(*Service).Ring(...)
	/…/internal/calls/calls.go:307 +0x4ca
created by github.com/teamup/teamup/internal/calls.(*Handler).CreateCall in goroutine 2018
	/…/internal/calls/routes.go:136 +0xa8f
```

Afterwards the PID was gone and `GET /healthz` returned nothing (curl exit 7, connection refused).

### Why this is severe

- **Unrecoverable.** `fatal error: concurrent map iteration and map write` is a runtime throw, not a panic. `gin.Recovery()` (`cmd/teamup/main.go:445`) cannot intercept it. One occurrence takes down every WebSocket session and every in-flight request on the instance.
- **Reached from an unrecovered goroutine.** `go h.service.Ring(...)` at `internal/calls/routes.go:136` has no recover, and `Ring` calls `BroadcastToUser` twice per invitee (`calls.go:307` and `:312`).
- **The trigger is ordinary.** Placing a call to someone whose socket is opening or closing — a page refresh, a reconnect, a phone moving between wifi and cellular. No malice and no unusual load required; the reproduction used four concurrent callers.

### Test shape

A `-race` unit test in `internal/websocket` is enough and will fail today: build a hub, register a connection for user U, then run (a) a goroutine looping `Register`/`Unregister` on additional connections for U and (b) a goroutine looping `BroadcastToUser(U, msg)`, for a few thousand iterations. Without the fix this reports a data race or dies with the fatal error.

## Comments

**Root cause.** As filed: `BroadcastToUser` took the read lock only long enough
to fetch `h.userConns[userID]` — the inner map itself, not a copy — released it,
and then ranged over that map while `Register` and `Unregister` were free to
write it.

**Fix.** `internal/websocket/hub.go:267` copies the connections into a slice
under `RLock` and enqueues after releasing, matching `BroadcastToChannel`.

**Test.** `internal/websocket/hub_test.go:277` —
`TestBroadcastingToSomebodyWhoIsReconnecting`, a `-race` test with one goroutine
looping `Register`/`Unregister` for a user while another loops
`BroadcastToUser` 5000 times.

**RED evidence.**

```
WARNING: DATA RACE
Write at 0x00c0003dc750 by goroutine 10:
  runtime.mapassign_faststr()
  websocket.(*Hub).Register()   internal/websocket/hub.go:106
Previous read at 0x00c0003dc750 by goroutine 11:
  runtime.mapIterStart()
  websocket.(*Hub).BroadcastToUser()   internal/websocket/hub.go:252
```

plus a second report pairing `mapdelete_faststr` in `Unregister` (`hub.go:135`)
with `mapIterNext` in `BroadcastToUser`. Green after the fix, over repeated runs.

**Audit of the other broadcast paths.** `BroadcastToChannel` was already correct.
`BroadcastToOrg` (`hub.go:280`) and `broadcastPresence` held `RLock` across their
whole body via `defer`, so they were never exposed to this — but they called
`Enqueue` under the lock, which the fourth checklist item asks against, so both
now snapshot and enqueue afterwards too. All four broadcast paths share one
shape. `TestBroadcastingToAnOrganizationWhilePeopleReconnect`
(`hub_test.go:322`) records that audit; it is a regression guard, **not** a
reproduction — it passed before the change, and the comment above it says so.
