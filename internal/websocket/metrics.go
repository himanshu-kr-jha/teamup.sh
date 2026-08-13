package websocket

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// wsMessagesDropped counts messages thrown away because a connection's send
// buffer was full. A drop means somebody's view of a channel, of who is online,
// or of an incoming call is now missing something, and until this existed there
// was no way for an operator to know it had happened at all: the only symptom
// was users reporting messages they never saw.
//
// Labelled by message type because the answers differ — a dropped presence
// update corrects itself on the next change, a dropped call.invited means a
// phone that never rang.
var wsMessagesDropped = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "teamup_ws_messages_dropped_total",
	Help: "WebSocket messages discarded because the client's send buffer was full",
}, []string{"type"})

// dropsBeforeClose is how many messages a connection may lose in a row before
// the server gives up on it and closes the socket.
//
// The count resets on every message that does make it into the buffer, so this
// is not "32 drops" but "32 attempts in a row where the writer moved nothing at
// all", on top of a buffer that already holds 256. A client that far behind is
// not slow, it is gone or wedged, and every further message widens a gap it has
// no way to notice: the missing messages are still in Postgres, but nothing
// tells the client to go and look. Closing turns a silent, permanent
// divergence into a reconnect and a refetch, which the client already does on
// an unclean close. The number is a judgement, not a measurement — high enough
// that an ordinary burst on a weak connection is absorbed by the buffer,
// low enough that a dead connection is reclaimed rather than accumulating.
const dropsBeforeClose = 32
