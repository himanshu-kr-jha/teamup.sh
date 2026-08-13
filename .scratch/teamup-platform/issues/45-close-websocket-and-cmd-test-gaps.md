# 45 — Test the inbound WebSocket path and include ./cmd/... in the suite

**What to build:** The suite is 286 green tests that never touch a client→server WebSocket frame, and `make test-unit` does not compile or run `./cmd/...` at all. That combination is what let a defect killing 100% of inbound WebSocket traffic ship as "completed". Close both gaps.

**Blocked by:** 33 — an end-to-end WebSocket test cannot pass until inbound payloads decode

**Status:** open

- [ ] Add `./cmd/...` to the `test-unit` target
- [ ] Add an end-to-end WebSocket test: real `websocket.Dial` against an `httptest` server, real frames in, assert persistence and delivery
- [ ] Cover each inbound type: `chat.send`, `chat.edit`, `chat.delete`, `chat.typing`, `chat.read`, `presence.update`, `call.invite`, `call.accept`, `call.decline`, `call.signaling`
- [ ] Add a concurrency test that registers and unregisters connections while broadcasting, run under `-race`

**Type:** task

## Evidence

### No test exercises an inbound handler

```
$ grep -rn "HandleSendMessage\|HandleTyping\|HandleCallInvite\|HandleEditMessage\|HandlePresenceUpdate\|HandleRead\|HandleDeleteMessage" --include="*_test.go" internal/
internal/websocket/hub_test.go:21:  func (stubPresence) HandlePresenceUpdate(context.Context, WSConnection, *protocol.Message) {}
```

The single hit is a stub method on a test double. Every test in `internal/websocket/hub_test.go` calls the broadcast side directly (`TestAChannelMessageReachesItsMembers` at `:177`, `TestDeliveryFollowsMembershipWithoutReconnecting` at `:222`), bypassing `Connection.handleMessage` (`hub.go:339`). Nothing drives a frame through `ReadLoop` → `handleMessage` → handler.

That is precisely the seam where ticket 33 lives: `msg.Payload.([]byte)` is always false for a JSON-decoded envelope, so all ten handlers return immediately. A test that hand-builds a `protocol.Message` and calls the service method would still pass, because it can set `Payload` to `[]byte` — something the real decode path never produces. The regression test must send bytes over a real socket.

`pkg/protocol` has no test file at all (`ls pkg/protocol/` → `messages.go`), so the envelope's decode behaviour is unasserted.

### `make test-unit` skips `./cmd/...`

`Makefile:95-97`:

```make
test-unit:
	TEAMUP_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
		go test -race -coverprofile=coverage.out -covermode=atomic ./internal/... ./pkg/...
```

`cmd/teamup/setup_test.go` and `cmd/teamup/livekit_proxy_test.go` never run. Running them explicitly passes (`ok github.com/teamup/teamup/cmd/teamup 1.103s`), so this is pure omission — but `cmd/teamup/main.go` is 875 lines holding all the wiring, the config layer, the route registration and the shutdown path, and none of it is covered by the command the project treats as its test suite.

### Baseline for comparison

With the full stack up and `TEAMUP_TEST_DATABASE_URL` set:

```
go test -race -v ./internal/... ./pkg/... ./cmd/...
  286 passed, 0 failed, 0 skipped
```

Database-backed tests genuinely execute — `internal/org` takes 38s, `internal/chat` 26s — so the suite is not silently skipping. It is honest about what it covers; it just does not cover the inbound socket or the binary's own wiring.
