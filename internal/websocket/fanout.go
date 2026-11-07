package websocket

import (
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/pkg/protocol"
)

// Cross-process fan-out.
//
// The hub's state is three in-process maps. Without something between the
// processes, a second replica does not split the load — it splits the company:
// a message sent by somebody on replica A reaches only the subset of the
// channel whose sockets happen to be on replica A, presence shows each replica
// only its own half, and calls do not ring across. Nothing surfaces it. The
// send succeeds, the fan-out finds nobody, and the people who did not receive
// it have no way to know a message existed. That is why the Helm chart used to
// refuse to render more than one replica, and why a deploy meant dropping
// everybody.
//
// Every process publishes each broadcast onto one Redis pub/sub channel and
// subscribes to that same channel. A received envelope is delivered through
// exactly the same code as a local one, so there is one definition of who a
// message reaches and it cannot drift between the local and remote paths.
//
// The four decisions worth arguing about:
//
// # 1. Who delivers, and exactly once
//
// A broadcast is delivered locally straight away and published afterwards; a
// process ignores any envelope carrying its own Origin. The alternative —
// publish only, and let every process including the sender deliver from its
// own subscription — has one code path and no origin check, but it puts a
// Redis round trip in front of local delivery and makes people on the sending
// replica lose messages whenever Redis blinks. Local delivery should not
// depend on a network hop, so the origin check earns its keep.
//
// Origin is a random id minted per process, not the pod name: a restarted pod
// reusing its name must not be mistaken for its predecessor.
//
// # 2. Loss and ordering
//
// Redis pub/sub is fire-and-forget. A subscriber that is down misses whatever
// was published while it was gone, and there is no replay. That is accepted
// here rather than papered over, for one reason and with one mitigation.
//
// The reason: the durable record is PostgreSQL, and these frames are
// notifications that it changed. A client that reconnects refetches, so a
// process being down loses nothing that its own clients will not immediately
// re-read — those clients were disconnected by the same event that made the
// subscriber miss the message. The delivery gap is co-terminous with the
// connection gap. Redis Streams with consumer groups would give at-least-once
// and replay, at the cost of per-consumer cursors, trimming, and a dedupe
// story at the client, to fix a window that mostly does not exist.
//
// The mitigation is for the window that does exist: this process staying up
// while its *subscription* dies — a Redis failover, a dropped connection.
// Then the clients are still connected, still believe they are current, and
// diverge silently, which is precisely the bug this file exists to remove. So
// a subscription that comes back after having been established once is treated
// as a gap of unknown size, and the hub ends its local sessions. Clients
// reconnect and refetch, which is the same recovery already used for a client
// that falls too far behind (see dropsBeforeClose). Blunt, and deliberately
// so: the alternative is silence.
//
// Ordering: Redis delivers to each subscriber in publication order over one
// connection, and both the publish side and the delivery side here are single
// goroutines, so frames from one process arrive at another in the order that
// process sent them. There is no global order across publishers — as there
// already was not across goroutines inside one process.
//
// # 3. One subscription per process
//
// Not one per chat channel. Per-channel subscriptions would mean SUBSCRIBE and
// UNSUBSCRIBE traffic on every join, leave and channel creation, plus
// bookkeeping to know when the last local subscriber went away — and it would
// do nothing for user- and org-addressed traffic, which is most of it. Every
// process receiving everything costs O(replicas x message rate); at the size
// this product is built for that is nothing. If a deployment ever outgrows it,
// the shard key is the organisation, and the change is confined to
// channelFor() and the subscribe call.
//
// # 4. Backpressure
//
// Both directions are bounded queues that drop rather than grow, matching what
// a connection's send buffer already does. Outbound, so that a stalled Redis
// cannot block the request goroutine that is broadcasting — chat must not stop
// working locally because Redis is slow. Inbound, so that a slow delivery step
// cannot turn the subscriber into an unbounded queue in memory, and so that
// the socket keeps being read: a receive loop that blocks stops draining the
// connection, Redis's own output buffer for that subscriber grows, and Redis
// eventually kills it. Drops are counted and logged, never silent.

// FanoutChannel is the Redis pub/sub channel every process publishes to and
// subscribes to.
const FanoutChannel = "teamup:ws:fanout"

// What an envelope addresses. These strings go on the wire, so they are part
// of the compatibility surface between replicas during a rolling upgrade: a
// process that meets a kind it does not know counts it and ignores it, rather
// than guessing and delivering to the wrong people.
const (
	fanoutKindChannel = "channel"
	fanoutKindUser    = "user"
	fanoutKindOrg     = "org"
	// fanoutKindDisconnect closes somebody's sockets, carrying no message.
	fanoutKindDisconnect = "disconnect"
)

// fanoutEnvelope is one broadcast, as one process tells the others about it.
//
// It carries the address rather than the resolved recipients. Each process
// works out who that means for its own connections — from its own roster and
// its own explicit subscriptions, neither of which the sender can see — so
// membership is resolved by the process that is going to deliver, and the
// remote path cannot disagree with the local one about who is in a channel.
//
// The field names are spelt out because this is a debugging surface: an
// operator watching `SUBSCRIBE teamup:ws:fanout` should be able to read it.
type fanoutEnvelope struct {
	Origin  string            `json:"origin"`
	Kind    string            `json:"kind"`
	Target  string            `json:"target"`
	Exclude string            `json:"exclude,omitempty"`
	Msg     *protocol.Message `json:"msg"`
}

const (
	// fanoutQueue bounds each direction. Deep enough to absorb a burst — a busy
	// channel, a call ringing a whole team — without dropping; shallow enough
	// that a wedged Redis or a stalled delivery costs bounded memory rather
	// than the process.
	fanoutQueue = 1024

	// fanoutPublishTimeout bounds one PUBLISH. Deliberately generous: the queue
	// in front of it is what protects callers from a slow Redis, so this only
	// has to stop a single command hanging for ever.
	fanoutPublishTimeout = 5 * time.Second

	// fanoutPingInterval is how often an idle subscription is proved alive.
	fanoutPingInterval = 30 * time.Second

	// fanoutSilenceBeforeReconnect is how long a subscription may hear nothing
	// at all — not a message, not the answer to a ping — before it is treated
	// as dead. Three intervals, the same slack the connection reaper allows a
	// client. A half-open TCP connection to Redis reads as perfect silence
	// rather than as an error, so without this the process would sit blocked on
	// a socket nobody is on the other end of, delivering nothing.
	fanoutSilenceBeforeReconnect = 3 * fanoutPingInterval

	// Reconnection backoff. Fast enough that an ordinary blip is invisible,
	// capped so that a Redis which is genuinely down is not hammered by every
	// replica at once. Jittered for the same reason.
	fanoutMinBackoff = 100 * time.Millisecond
	fanoutMaxBackoff = 5 * time.Second
)

// Fanout carries hub broadcasts between processes over Redis pub/sub.
//
// The zero value is not usable; see NewFanout. A hub with no Fanout attached
// behaves exactly as it did before this existed, which is what makes a
// single-replica deployment and every test that does not care about replicas
// unaffected.
type Fanout struct {
	redis   *redis.Client
	logger  zerolog.Logger
	channel string

	// hub is where received envelopes are delivered. Set by Hub.SetFanout.
	hub *Hub

	outbound chan *fanoutEnvelope
	inbound  chan *fanoutEnvelope

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	startOnce sync.Once
	closeOnce sync.Once

	// ready is closed once a subscription has been confirmed by the server, so
	// that startup can say whether cross-process delivery is actually working
	// rather than merely configured.
	readyOnce sync.Once
	ready     chan struct{}

	// subscribed records that a subscription was established at least once, so
	// that a later one can be recognised as a recovery from a gap rather than
	// as the first connection. Only the subscriber goroutine touches it.
	subscribed bool
}

// NewFanout prepares cross-process delivery over the given Redis. Nothing
// happens until Start.
func NewFanout(client *redis.Client, logger zerolog.Logger) *Fanout {
	ctx, cancel := context.WithCancel(context.Background())
	return &Fanout{
		redis:    client,
		logger:   logger,
		channel:  FanoutChannel,
		outbound: make(chan *fanoutEnvelope, fanoutQueue),
		inbound:  make(chan *fanoutEnvelope, fanoutQueue),
		ctx:      ctx,
		cancel:   cancel,
		ready:    make(chan struct{}),
	}
}

// Start begins publishing and subscribing. Safe to call more than once.
func (f *Fanout) Start() {
	f.startOnce.Do(func() {
		f.wg.Add(3)
		go f.publishLoop()
		go f.subscribeLoop()
		go f.deliverLoop()
	})
}

// WaitReady blocks until a subscription has been confirmed by Redis, or the
// context expires.
//
// Pub/sub has no backlog: anything published before the SUBSCRIBE has been
// registered is gone, not queued. So there is a real difference between
// "Start has been called" and "this process is receiving", and both startup
// and any test that publishes immediately need the second one.
func (f *Fanout) WaitReady(ctx context.Context) error {
	select {
	case <-f.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close unsubscribes and stops, giving whatever is already queued a chance to
// go out first.
//
// Draining matters at shutdown specifically: the last thing a departing
// process publishes is every user it was serving going offline, and their
// colleagues on the other replicas will otherwise be shown a green dot for a
// person who has gone until the presence entry expires.
func (f *Fanout) Close(ctx context.Context) error {
	var err error
	f.closeOnce.Do(func() {
		err = f.drain(ctx)
		f.cancel()
		err = f.wait(ctx, err)
	})
	return err
}

// drain waits for the outbound queue to empty.
func (f *Fanout) drain(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		if len(f.outbound) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			f.logger.Warn().
				Int("queued", len(f.outbound)).
				Msg("fan-out: shutting down with publications still queued")
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// wait joins the goroutines, keeping the first error seen.
func (f *Fanout) wait(ctx context.Context, prior error) error {
	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return prior
	case <-ctx.Done():
		if prior != nil {
			return prior
		}
		return ctx.Err()
	}
}

// publish offers an envelope to the other processes, and gives up rather than
// waiting.
//
// Called from the broadcast paths, which run on request goroutines: a Redis
// that has stopped answering must cost a counted drop, not a stalled request.
func (f *Fanout) publish(env *fanoutEnvelope) {
	select {
	case <-f.ctx.Done():
		// Shutting down. Local delivery has already happened; there is nobody
		// left here to publish on behalf of.
		return
	default:
	}

	select {
	case f.outbound <- env:
		wsFanoutPublished.WithLabelValues(env.Kind).Inc()
	default:
		wsFanoutDropped.WithLabelValues("outbound_full").Inc()
		f.logger.Warn().
			Str("kind", env.Kind).
			Str("target", env.Target).
			Str("type", env.Msg.Type).
			Msg("fan-out: dropping a publication, Redis is not keeping up — the other replicas will not see this")
	}
}

func (f *Fanout) publishLoop() {
	defer f.wg.Done()

	for {
		select {
		case <-f.ctx.Done():
			return
		case env := <-f.outbound:
			f.publishOne(env)
		}
	}
}

func (f *Fanout) publishOne(env *fanoutEnvelope) {
	payload, err := json.Marshal(env)
	if err != nil {
		// Every payload here is a protocol.Message whose own payload is already
		// raw JSON, so this cannot happen; counting it is cheaper than assuming.
		wsFanoutDropped.WithLabelValues("encode").Inc()
		f.logger.Error().Err(err).Str("type", env.Msg.Type).Msg("fan-out: could not encode a publication")
		return
	}

	// Not derived from f.ctx: a publication that is already in flight when
	// shutdown starts should finish, which is the whole point of draining.
	ctx, cancel := context.WithTimeout(context.Background(), fanoutPublishTimeout)
	defer cancel()

	if err := f.redis.Publish(ctx, f.channel, payload).Err(); err != nil {
		wsFanoutDropped.WithLabelValues("publish_failed").Inc()
		f.logger.Warn().Err(err).
			Str("kind", env.Kind).
			Str("type", env.Msg.Type).
			Msg("fan-out: publish failed — this message reached only the people on this replica")
	}
}

// subscribeLoop keeps a subscription up for as long as the process runs.
func (f *Fanout) subscribeLoop() {
	defer f.wg.Done()

	backoff := fanoutMinBackoff
	for {
		if f.ctx.Err() != nil {
			return
		}

		if f.session() {
			// The subscription lived, so whatever went wrong is new; do not
			// hold the previous outage's backoff against it.
			backoff = fanoutMinBackoff
		}

		if f.ctx.Err() != nil {
			return
		}

		// Jittered so that every replica does not come back at the same
		// instant on the same Redis.
		wait := backoff/2 + time.Duration(rand.Int63n(int64(backoff/2)+1))
		select {
		case <-f.ctx.Done():
			return
		case <-time.After(wait):
		}
		if backoff < fanoutMaxBackoff {
			backoff *= 2
		}
	}
}

// session holds one subscription until it fails or the process stops. It
// reports whether the subscription was ever established.
func (f *Fanout) session() bool {
	pubsub := f.redis.Subscribe(f.ctx, f.channel)
	defer pubsub.Close()

	// Anything heard from Redis, including the answer to a ping, proves the
	// subscription is alive. Written by this goroutine, read by the watchdog.
	var lastHeard atomic.Int64
	lastHeard.Store(time.Now().UnixNano())

	// The watchdog is also what makes shutdown work. Receive takes no read
	// deadline and does not observe the context — closing the subscription is
	// the only thing that unblocks it.
	watching := make(chan struct{})
	defer close(watching)
	go f.watch(pubsub, &lastHeard, watching)

	// Subscribe only queues the command; the server's confirmation comes back
	// as the first frame. Until it arrives this process is not receiving
	// anything, and pub/sub has no backlog to catch up from.
	if _, err := pubsub.Receive(f.ctx); err != nil {
		if f.ctx.Err() == nil {
			f.logger.Warn().Err(err).Str("channel", f.channel).Msg("fan-out: could not subscribe")
		}
		return false
	}

	if f.subscribed {
		// We were subscribed, lost it, and are back. Whatever was published in
		// between is gone — pub/sub does not replay — and we cannot know
		// whether that was nothing or everything.
		wsFanoutResubscribes.Inc()
		f.logger.Warn().Str("channel", f.channel).
			Msg("fan-out: resubscribed after losing Redis; messages published during the gap were not received")
		if f.hub != nil {
			f.hub.ResyncAfterGap()
		}
	} else {
		f.subscribed = true
		f.logger.Info().Str("channel", f.channel).Msg("fan-out: subscribed")
		f.markReady()
	}
	wsFanoutSubscribed.Set(1)
	defer wsFanoutSubscribed.Set(0)

	for {
		frame, err := pubsub.Receive(f.ctx)
		if err != nil {
			if f.ctx.Err() == nil {
				f.logger.Warn().Err(err).Msg("fan-out: subscription lost, reconnecting")
			}
			return true
		}
		lastHeard.Store(time.Now().UnixNano())

		if msg, ok := frame.(*redis.Message); ok {
			f.accept(msg.Payload)
		}
		// *redis.Subscription and *redis.Pong are liveness, already recorded.
	}
}

// watch ends a subscription that has gone quiet or cannot be pinged, and ends
// it on shutdown. Closing the subscription is what unblocks the receive.
func (f *Fanout) watch(pubsub *redis.PubSub, lastHeard *atomic.Int64, done <-chan struct{}) {
	ticker := time.NewTicker(fanoutPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-f.ctx.Done():
			_ = pubsub.Close()
			return
		case <-ticker.C:
			if silence := time.Since(time.Unix(0, lastHeard.Load())); silence > fanoutSilenceBeforeReconnect {
				f.logger.Warn().Dur("silence", silence).
					Msg("fan-out: nothing heard from Redis, dropping the subscription")
				_ = pubsub.Close()
				return
			}
			if err := pubsub.Ping(f.ctx); err != nil {
				if f.ctx.Err() == nil {
					f.logger.Warn().Err(err).Msg("fan-out: could not ping Redis, dropping the subscription")
				}
				_ = pubsub.Close()
				return
			}
		}
	}
}

// accept takes one published envelope off the wire.
//
// Decoding and the origin check happen here, on the receive loop, because both
// are cheap and doing them before the queue keeps a process's own publications
// from occupying room meant for other people's.
func (f *Fanout) accept(payload string) {
	var env fanoutEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		wsFanoutDropped.WithLabelValues("decode").Inc()
		f.logger.Warn().Err(err).Msg("fan-out: could not decode a publication")
		return
	}

	// Anything at all may publish onto a Redis channel, and a malformed
	// envelope decodes happily into a struct with a nil message. Delivering
	// that dereferences nil on a goroutine with no recover, which takes the
	// process down — a far worse outcome than whatever the sender intended.
	if env.Msg == nil {
		wsFanoutDropped.WithLabelValues("decode").Inc()
		f.logger.Warn().Str("kind", env.Kind).Msg("fan-out: a publication carried no message")
		return
	}

	// Our own. Delivered locally when it was broadcast; delivering it again
	// here is how everybody on the sending replica sees every message twice.
	if f.hub != nil && env.Origin == f.hub.nodeID {
		return
	}

	wsFanoutReceived.WithLabelValues(env.Kind).Inc()

	select {
	case f.inbound <- &env:
	default:
		wsFanoutDropped.WithLabelValues("inbound_full").Inc()
		f.logger.Warn().
			Str("kind", env.Kind).
			Str("type", env.Msg.Type).
			Msg("fan-out: dropping a received publication, delivery is not keeping up")
	}
}

// deliverLoop hands received envelopes to the hub, one at a time.
//
// One goroutine, not a pool: it preserves the order the sending process
// published in, which is the order the sender's users saw. Delivery does a
// roster lookup, so it can block on the database — that is exactly why it is
// not done on the receive loop, and why the queue in front of it is bounded.
func (f *Fanout) deliverLoop() {
	defer f.wg.Done()

	for {
		select {
		case <-f.ctx.Done():
			return
		case env := <-f.inbound:
			if f.hub != nil {
				f.hub.deliverEnvelope(env)
			}
		}
	}
}

func (f *Fanout) markReady() {
	f.readyOnce.Do(func() { close(f.ready) })
}
