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

// The cross-process fan-out, made visible.
//
// The failure this replaces was silent by construction: with the hub's state
// in process, a second replica delivered a message to half the company and
// reported success. Anything that can now go wrong between processes — a
// publication dropped because Redis is not keeping up, a received one dropped
// because delivery is not, a subscription lost — has to be countable, or the
// same class of bug comes back wearing different clothes.
var (
	wsFanoutPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_ws_fanout_published_total",
		Help: "Broadcasts queued for publication to the other replicas",
	}, []string{"kind"})

	wsFanoutReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_ws_fanout_received_total",
		Help: "Broadcasts received from another replica",
	}, []string{"kind"})

	// Labelled by reason because they mean different things to an operator: a
	// full queue is this process not keeping up, a failed publish is Redis, a
	// decode failure or an unknown kind is a version skew mid-rollout.
	wsFanoutDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_ws_fanout_dropped_total",
		Help: "Cross-replica broadcasts that were not carried",
	}, []string{"reason"})

	// A rising count means clients are being disconnected to resync; see the
	// loss discussion in fanout.go.
	wsFanoutResubscribes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "teamup_ws_fanout_resubscribes_total",
		Help: "Times this process had to re-establish its fan-out subscription",
	})

	// 0 on a running process means it is deaf to the other replicas, which
	// looks like nothing at all from the outside. Worth alerting on.
	wsFanoutSubscribed = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "teamup_ws_fanout_subscribed",
		Help: "1 while this process holds a fan-out subscription",
	})
)

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
