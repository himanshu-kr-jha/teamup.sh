# 38 — Wait for the drain to finish before exiting on SIGTERM

**What to build:** `runServer` calls `srv.Shutdown` from a goroutine and never waits for it. `ListenAndServe` returns the instant shutdown begins, `runServer` returns, the deferred pool closes run, and the process exits — roughly 0.1s after SIGTERM, with the 30-second drain abandoned. Make the shutdown path block until the drain completes, and close WebSocket sessions cleanly.

**Blocked by:** None — can start immediately

**Status:** open — the drain is fixed and measured; two checklist items are blocked on `internal/websocket`

- [x] Failing test: SIGTERM with a slow request in flight; assert the response completes and the process outlives it
- [x] Wait on the shutdown goroutine (channel or `errgroup`) before returning from `runServer`
- [ ] Close registered WebSocket connections with a close frame so clients back off instead of stampeding — sessions are now closed deliberately during the drain and unregistered from the hub, but there is still no RFC 6455 close frame. See Comments.
- [ ] Stop `Hub.Run` and the upload sweeper on shutdown — the sweeper stops after the drain; `Hub.Run` has no stop and cannot be given one without editing `internal/websocket`. See Comments.
- [x] Close the DB pool and Redis client only after the drain returns

**Type:** bug

## Evidence

`cmd/teamup/main.go:608-627`:

```go
// Graceful shutdown
go func() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info().Msg("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error().Err(err).Msg("Server forced to shutdown")
	}
}()

logger.Info().Str("port", cfg.Port).Msg("Server starting")
if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
	return fmt.Errorf("server: %w", err)
}

logger.Info().Msg("Server stopped")
return nil
```

Nothing joins that goroutine. `srv.Shutdown` causes `ListenAndServe` to return `http.ErrServerClosed` immediately, the `if` is skipped, and `runServer` returns. That runs the deferred `dbPool.Close()` (`main.go:335`), `redisClient.Close()` (`main.go:350`) and `stopSweep()` (`main.go:583`), then `main` returns and the process exits — all while `srv.Shutdown` is still trying to drain. In-flight handlers lose their database pool underneath them.

### Reproduction (verified)

Three WebSocket clients connected to a live server, then `kill -TERM <pid>`:

```
3 websocket clients connected
sending SIGTERM to 115444
process exited after 0.11s
```

Server log, consecutive lines:

```
cmd/teamup/main.go:613 > Shutting down server...
cmd/teamup/main.go:626 > Server stopped
```

`main.go:626` is the line *after* `ListenAndServe` returns, so `runServer` completed while the drain was still in progress. The 30-second timeout at `main.go:614` is never reached because nobody waits on it.

### Impact

On a Kubernetes rolling deploy or a `docker compose up -d` replacement, every request in flight when the pod receives SIGTERM is severed rather than completed. Clients see connection resets instead of responses.

WebSocket sessions are worse: they are hijacked connections that `srv.Shutdown` does not track at all, and nothing in the hub closes them. The three test clients were cut off with no `conn.close` frame. Combined with the exponential-backoff reconnect in the WebSocket decision, every client of the departing instance reconnects on an abnormal-closure path simultaneously.

### Test shape

Register a handler that sleeps ~2s, start the server, issue that request, send SIGTERM ~100ms later. Assert the client receives a complete response and that the process was still alive when it arrived. Today the process is gone in ~0.1s and the request fails.

## Comments

### Root cause

Exactly as filed: `srv.Shutdown` ran in a goroutine nobody joined. `ListenAndServe` returns `http.ErrServerClosed` the instant shutdown begins, so `runServer` fell straight through to `return nil`, ran the deferred `dbPool.Close()`, `redisClient.Close()` and `stopSweep()`, and the process exited while the drain was still in progress. The 30-second timeout was never reached because nothing waited on it.

### Measured, before and after

Reproduced with the real binary: three WebSocket sessions open, one `POST /api/auth/login` in flight whose body is dribbled out over 2s (so the handler is inside `ShouldBindJSON` — an in-flight request that needs no test-only slow endpoint), then `SIGTERM`.

Before:

```
process exited 6ms after SIGTERM
in-flight request: status=0 err=Post "http://127.0.0.1:38355/api/auth/login":
    write tcp 127.0.0.1:42614->127.0.0.1:38355: use of closed network connection
session 0 closed 6ms after SIGTERM (0s before the process exited): EOF
session 1 closed 6ms after SIGTERM (0s before the process exited): EOF
session 2 closed 6ms after SIGTERM (0s before the process exited): EOF
presence after shutdown = "online"
```

6ms, not the 0.11s on the ticket — the audit's figure included process teardown. The request was severed mid-flight, and every session was still recorded online in Redis afterwards because nothing ever unregistered them.

After:

```
process exited 2.095s after SIGTERM
in-flight request: status=401 err=<nil> after=2.014s
session 0 closed 1ms after SIGTERM (2.094s before the process exited)
session 1 closed 1ms after SIGTERM (2.094s before the process exited)
session 2 closed 1ms after SIGTERM (2.094s before the process exited)
presence after shutdown = "offline"
```

The process now leaves when the work is done rather than on a timer: 2.1s, which is how long the in-flight request had left. With a 15s grace period configured it did not wait 15s — it waited for the drain.

### The fix

`cmd/teamup/main.go` — `runServer`'s tail is now a sequence rather than two racing goroutines:

1. `signal.Notify` is armed **before** the listener opens, so a SIGTERM during a slow start is not missed.
2. `ListenAndServe` moves to a goroutine reporting through a `serving` channel; `runServer` selects on that and the signal.
3. On a signal, one `drainCtx` with `cfg.ShutdownGracePeriod` bounds everything that follows.
4. WebSocket sessions are closed **first**, while Postgres and Redis are still open — unregistering a session writes presence and broadcasts it.
5. `srv.Shutdown(drainCtx)` is called and **waited on**. Then `<-serving`, then `sessions.Wait(drainCtx)`.
6. Only then does `runServer` return, so the deferred `dbPool.Close`, `redisClient.Close` and `stopSweep` finally run after the drain instead of during it.

A failed drain returns the error, so the process exits non-zero and the failure is visible rather than silent.

New config: **`TEAMUP_SHUTDOWN_GRACE`, a duration, default `30s`.** It must be lower than the orchestrator's own kill deadline (Kubernetes `terminationGracePeriodSeconds`, Compose `stop_grace_period`) or the drain is cut short by SIGKILL.

`cmd/teamup/shutdown.go` (new) holds `wsSessions`. A WebSocket is a hijacked connection: `net/http` untracks it at the upgrade, so `Server.Shutdown` neither waits for it nor closes it. The tracker wraps the `/ws` route, keeps each request's cancel func, ends them all on shutdown, and refuses new upgrades with `503` + `Retry-After: 5` once draining.

### What I chose for WebSockets, and what is still owed

Ending a session through its request context is what lets `Hub.Unregister` run — presence flips to offline and the user's colleagues are told — all inside the grace window with Redis still open. That is the difference between "told" and "vanished", and it is what the `presence after shutdown = "offline"` assertion pins down.

It is **not** a close frame. The client still sees an abnormal closure (EOF/1006), because `nhooyr.io/websocket` hard-closes the TCP connection when a read context is cancelled (`Conn.timeoutLoop` → `c.close()` → `rwc.Close()`, no handshake). Sending `1001 going away` needs somebody holding the `*websocket.Conn` to call `Close` *before* the context is cancelled, and the only thing that holds them is `Hub.connections`. That is a `Hub.Shutdown(ctx)` in `internal/websocket`, which this worktree was told not to touch. Same reason `Hub.Run`'s infinite `for { select { case <-ticker.C } }` still has no stop — cosmetic here, since the process is about to exit, but it belongs with the same change.

**Follow-up for whoever owns `internal/websocket`:** add `Hub.Shutdown(ctx)` that walks `h.connections`, calls `conn.WS.Close(websocket.StatusGoingAway, "server shutting down")` on each, then cancels; and give `Hub.Run` a stop channel. `cmd/teamup` should then call it in place of `sessions.CloseAll()`. Worth noting the same bug exists on the token-expiry path in `WSHandler` today: it calls `conn.cancel()` *before* `wsConn.Close(...)`, so that close frame is never sent either.

### Tests

- `cmd/teamup/shutdown_test.go:26` — `TestSIGTERMDrainsInFlightRequestsAndClosesSessions`. Drives the real binary against the live services. Asserts the in-flight request completes (401, a complete response), the process outlives it, the exit is bounded by the grace period, each session is closed by the shutdown sequence rather than by the process exiting, and presence is `offline` afterwards.
- `cmd/teamup/wssessions_test.go` — unit coverage for `wsSessions`: `CloseAll` ends live sessions, new sessions are refused while draining, `Wait` blocks until handlers return, sessions that end by themselves are forgotten.
- `cmd/teamup/server_harness_test.go` — shared subprocess harness (also used by #39). It clones its own `<template>_server` database rather than using `TEAMUP_TEST_DATABASE_URL` directly, because a long-lived server pool on the template would break every other package's `CREATE DATABASE ... TEMPLATE`.

Each WebSocket assertion was checked for discrimination by disabling only `sessions.CloseAll()`: the process then hung the full 15s grace period, every session was dropped at exit, and presence stayed `"online"`.

### Deliberately not done

- No pre-drain readiness delay (mark unready, sleep, *then* close the listener) to let load-balancer endpoints propagate. It is the standard extra step, but `srv.Shutdown` closes the listener immediately, so a probe during the drain fails at the TCP level anyway. Worth adding alongside the Helm chart if endpoint propagation turns out to matter.
