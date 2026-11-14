package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"

	"github.com/teamup/teamup/internal/observability"
	"github.com/teamup/teamup/pkg/protocol"
)

type Hub struct {
	services        ServiceInterface
	presenceService PresenceServiceInterface
	authService     AuthServiceInterface
	roster          ChannelRosterInterface
	calls           CallInviteDeliverer
	logger          zerolog.Logger

	// nodeID identifies this process for as long as it runs.
	//
	// Random per process rather than the hostname or pod name: it is what
	// separates our own publications from other people's, and what tags our
	// entries in the shared session registry. A restarted pod that reused its
	// name would inherit the sessions its predecessor never got to clean up,
	// and would keep everybody it had been serving showing online.
	nodeID string

	// fanout carries broadcasts to and from the other replicas. Nil in a
	// single-process deployment and in tests, where the hub behaves exactly as
	// it did before cross-process delivery existed.
	fanout *Fanout

	// sessions counts a person's live sockets across every process, so that
	// "are they connected" and "have they gone offline" are cluster questions
	// rather than questions about this process's maps. Nil when the presence
	// implementation does not offer it.
	sessions PresenceSessions

	mu          sync.RWMutex
	connections map[string]*Connection            // connectionID -> Connection
	userConns   map[string]map[string]*Connection // userID -> connectionID -> Connection
	channelSubs map[string]map[string]*Connection // channelID -> userID -> Connection

	stop     chan struct{}
	stopOnce sync.Once
}

type Connection struct {
	ID            string
	userID        string
	orgID         string
	WS            *websocket.Conn
	SendCh        chan *protocol.Message
	Subscriptions map[string]bool // channelID -> true
	CloseOnce     sync.Once
	ctx           context.Context
	cancel        context.CancelFunc
	hub           *Hub
	logger        zerolog.Logger

	// consecutiveDrops counts messages discarded since the last one that made
	// it into the buffer. It measures whether the writer is moving at all,
	// which is what separates a client absorbing a burst from one that has
	// stopped reading; see dropsBeforeClose.
	consecutiveDrops atomic.Int64

	// lastSeen is when this connection last said anything, as Unix nanoseconds.
	// Atomic because the reader writes it and the reaper reads it.
	lastSeen atomic.Int64

	// frames and typingAt limit what this connection sends; see limits.go.
	// Only the read loop touches them.
	frames   frameBucket
	typingAt map[string]time.Time
}

// staleAfter is how long a connection may go without saying anything before it
// is treated as gone.
//
// The writer pings every 30 seconds and a live client answers immediately, so
// this allows three missed pings before giving up. These are application-level
// frames over an established TCP connection, not TCP keepalives: when a client
// vanishes without a FIN — a suspended laptop, an expiring NAT entry, a phone
// changing network — writes keep succeeding into the socket buffer for a long
// time, so nothing else notices. Three is slack enough for a stalled tab or a
// tunnel that hiccups, and short enough that a user's colleagues are not shown
// a stale green dot for minutes.
const staleAfter = 95 * time.Second

func (c *Connection) UserID() string {
	return c.userID
}

func (c *Connection) OrgID() string {
	return c.orgID
}

// Enqueue offers a message to the connection's writer, and gives up rather
// than waiting: one slow client must not hold up a broadcast to everybody else.
//
// A finished connection swallows the message. SendCh is never closed, because
// this is called by whoever holds a pointer taken from the hub maps, long after
// the hub lock was released and possibly after the connection ended — and a
// send on a closed channel panics however the select is written, from
// goroutines that have no recover.
func (c *Connection) Enqueue(msg *protocol.Message) {
	select {
	case <-c.ctx.Done():
		return // nobody is reading this any more
	default:
	}

	select {
	case c.SendCh <- msg:
		c.consecutiveDrops.Store(0)
	default:
		c.dropMessage(msg)
	}
}

// dropMessage records a message the connection had no room for, and gives up
// on a connection that has stopped taking anything at all.
//
// The drop used to be an empty default branch: no counter, no log, no signal to
// the client. The messages are still in Postgres, so the user's view was merely
// wrong rather than obviously broken, and nothing on either side knew. Now it
// is counted and logged, and a client that is provably not keeping up is closed
// so that it reconnects and refetches instead of diverging for as long as it
// stays connected.
func (c *Connection) dropMessage(msg *protocol.Message) {
	wsMessagesDropped.WithLabelValues(msg.Type).Inc()
	inARow := c.consecutiveDrops.Add(1)

	c.logger.Warn().
		Str("conn", c.ID).
		Str("user", c.userID).
		Str("type", msg.Type).
		Int64("consecutive", inARow).
		Msg("dropping message: the client is not reading fast enough")

	if inARow < dropsBeforeClose {
		return
	}

	c.logger.Warn().
		Str("conn", c.ID).
		Str("user", c.userID).
		Int64("consecutive", inARow).
		Msg("closing socket: the client is too far behind, reconnecting will refetch")

	// Cancelled here rather than only inside finish, so that every other
	// broadcast stops offering to this connection from the next call onwards.
	// The close itself writes a close frame to the network, which must not
	// happen on a broadcast goroutine that still has other people to deliver
	// to — one wedged client must not hold up delivery to everybody else,
	// which is the whole reason Enqueue does not block in the first place.
	c.cancel()
	go c.finish()
}

func NewHub(
	services ServiceInterface,
	presenceService PresenceServiceInterface,
	authService AuthServiceInterface,
	logger zerolog.Logger,
) *Hub {
	hub := &Hub{
		services:        services,
		presenceService: presenceService,
		authService:     authService,
		logger:          logger,
		nodeID:          uuid.NewString(),
		connections:     make(map[string]*Connection),
		userConns:       make(map[string]map[string]*Connection),
		channelSubs:     make(map[string]map[string]*Connection),
		stop:            make(chan struct{}),
	}

	// Asked for rather than required, as PresenceRefresher is: the presence
	// interface is implemented by test doubles in several packages, and a
	// presence store that is not shared between processes has no cluster-wide
	// session count to offer. Without it the hub answers from its own maps,
	// which is exactly the behaviour of a single-replica deployment.
	if sessions, ok := presenceService.(PresenceSessions); ok {
		hub.sessions = sessions
	}

	return hub
}

// SetFanout wires cross-process delivery. Without it a broadcast reaches only
// the sockets this process owns.
//
// Call it before Fanout.Start. The subscriber and delivery goroutines read the
// hub this attaches, and the only thing ordering that read against this write
// is the goroutines not existing yet.
func (h *Hub) SetFanout(f *Fanout) {
	h.fanout = f
	f.hub = h
}

// NodeID identifies this process in the fan-out and the session registry.
func (h *Hub) NodeID() string { return h.nodeID }

// sessionID names one socket uniquely across the whole cluster. Connection ids
// are only unique within a process, which is not enough once several are
// writing the same registry.
func (h *Hub) sessionID(conn *Connection) string { return h.nodeID + ":" + conn.ID }

// Run reaps dead connections until Stop is called.
//
// The stop channel is what lets the goroutine end: this used to be a for/select
// with a single case and no way out, so the reaper outlived every shutdown.
func (h *Hub) Run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.cleanupStaleConnections()
		}
	}
}

// Stop ends Run. Safe to call more than once, and from anywhere.
func (h *Hub) Stop() {
	h.stopOnce.Do(func() { close(h.stop) })
}

// Register adds a connection and announces the user as online.
//
// The announcement happens after the lock is released. sync.RWMutex is not
// reentrant, so calling broadcastPresence — which takes a read lock — while
// still holding the write lock deadlocked the hub permanently on the very
// first connection, taking presence, live messages and call ringing with it.
func (h *Hub) Register(conn *Connection) {
	// Opening the socket is the first thing they were seen doing, and without
	// this the reaper would judge every new connection against a zero clock.
	conn.touch()

	h.mu.Lock()
	if _, already := h.connections[conn.ID]; !already {
		observability.RecordWSConnection(1)
	}
	h.connections[conn.ID] = conn

	if h.userConns[conn.UserID()] == nil {
		h.userConns[conn.UserID()] = make(map[string]*Connection)
	}
	h.userConns[conn.UserID()][conn.ID] = conn

	h.mu.Unlock()

	// Claim this socket in the shared registry before saying they are online,
	// so that a concurrent unregister on another replica counting what is left
	// can already see it.
	if h.sessions != nil {
		if err := h.sessions.AddSession(conn.UserID(), h.sessionID(conn)); err != nil {
			h.logger.Warn().Err(err).
				Str("user", conn.UserID()).
				Msg("could not record the session: the other replicas may not see this person as reachable")
		}
	}

	h.presenceService.SetOnline(conn.UserID(), conn.OrgID())
	h.broadcastPresence(conn.OrgID(), conn.UserID(), "online", "")

	// Anybody who called while they were away may still be waiting, so their
	// phone rings now rather than never.
	if h.calls != nil {
		if userID, err := uuid.Parse(conn.UserID()); err == nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				h.calls.DeliverPendingInvitations(ctx, userID)
			}()
		}
	}
}

// ConnectionCount is how many sockets this process holds open: every tab and
// phone of everybody connected here.
func (h *Hub) ConnectionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}

// Unregister removes a connection, announcing the user as offline once their
// last one has gone — anywhere, not just here. As in Register, the
// announcement is made after the lock is released.
//
// "Their last one" used to mean the last one in this process's map, which with
// several replicas is a different and wrong question: somebody closing a tab
// served by replica A would be published as offline while their phone was
// still connected to replica B, and their colleagues would watch them
// disappear. The registry is what makes the count a cluster count.
func (h *Hub) Unregister(conn *Connection) {
	h.mu.Lock()
	// The /metrics gauge of open sockets, kept here where they are counted. A
	// connection unregistered twice is taken off once.
	if _, ok := h.connections[conn.ID]; ok {
		observability.RecordWSConnection(-1)
	}
	delete(h.connections, conn.ID)

	wentOffline := false
	if userConns, ok := h.userConns[conn.UserID()]; ok {
		delete(userConns, conn.ID)
		if len(userConns) == 0 {
			delete(h.userConns, conn.UserID())
			wentOffline = true
		}
	}

	for channelID := range conn.Subscriptions {
		if channelSubs, ok := h.channelSubs[channelID]; ok {
			delete(channelSubs, conn.UserID())
			if len(channelSubs) == 0 {
				delete(h.channelSubs, channelID)
			}
		}
	}
	h.mu.Unlock()

	// Give up this socket's claim whatever else is true, so the registry does
	// not have to wait for the entry to expire to stop counting it. Only ever
	// our own: a process removes the sessions it owns and nothing else — see
	// cleanupStaleConnections for the other half of that rule.
	lastAnywhere := wentOffline
	if h.sessions != nil {
		remaining, err := h.sessions.RemoveSession(conn.UserID(), h.sessionID(conn))
		if err != nil {
			// Fall back to the local answer, which is what this did before the
			// registry existed. Redis being unreachable means SetOffline below
			// will not land either, and the entry carries a TTL for exactly
			// this case.
			h.logger.Warn().Err(err).
				Str("user", conn.UserID()).
				Msg("could not release the session: falling back to this process's own view of who is left")
		} else {
			// Whoever takes the count to zero announces it — not whoever
			// happened to empty this process's map.
			//
			// Those are different sessions, and a shutdown is where they come
			// apart: every session of a person is cancelled at once and each
			// unregisters on its own goroutine. All of them leave the map, then
			// all of them reach the registry, and the one that saw the map go
			// empty can get there first and still find the others' claims.
			// Judged on wentOffline it would say nothing, the others would have
			// already decided it was not their business, and the person's
			// colleagues would be left looking at a green dot for somebody
			// whose server has gone.
			//
			// The map is still consulted, freshly: the count reaching zero and
			// this process having nothing left are two different facts, and a
			// claim that failed to be written would otherwise let a live socket
			// be announced as gone.
			lastAnywhere = remaining == 0 && !h.hasLocalConnections(conn.UserID())
		}
	}

	if lastAnywhere {
		h.presenceService.SetOffline(conn.UserID(), conn.OrgID())
		h.broadcastPresence(conn.OrgID(), conn.UserID(), "offline", "")
	}
}

// SetCallInviteDeliverer wires the check for calls waiting on somebody.
func (h *Hub) SetCallInviteDeliverer(c CallInviteDeliverer) { h.calls = c }

// IsConnected reports whether somebody has a session open anywhere in the
// cluster, which is what separates a phone that is ringing from one that is
// merely being called.
//
// Anywhere, not here. Notifications skip a web push for somebody the socket
// has already reached, and calls tell the caller whether anybody is there;
// answered per replica, both were wrong for everybody whose socket was
// somewhere else — a duplicate push in one direction, a call reported as
// unreachable to somebody who was sitting there in the other.
//
// The local map is consulted first because it is free and, for the common case
// of a busy channel on a single replica, always the answer. The registry is
// only asked about people this process cannot see. It fails towards "not
// connected", which is what the callers already fail towards: a duplicate
// notification rather than a missing one.
func (h *Hub) IsConnected(userID string) bool {
	local := h.hasLocalConnections(userID)
	if local || h.sessions == nil {
		return local
	}

	connected, err := h.sessions.AnySession(userID)
	if err != nil {
		h.logger.Warn().Err(err).Str("user", userID).
			Msg("could not ask whether they are connected elsewhere; assuming not")
		return false
	}
	return connected
}

// hasLocalConnections reports whether this process still holds a socket for
// somebody. Asked at the moment it matters, unlike a snapshot taken earlier
// under a lock that has since been released.
func (h *Hub) hasLocalConnections(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.userConns[userID]) > 0
}

// SetChannelRoster wires the lookup that decides who a channel message reaches.
func (h *Hub) SetChannelRoster(r ChannelRosterInterface) { h.roster = r }

func (h *Hub) SubscribeToChannel(conn *Connection, channelID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conn.Subscriptions[channelID] = true

	if h.channelSubs[channelID] == nil {
		h.channelSubs[channelID] = make(map[string]*Connection)
	}
	h.channelSubs[channelID][conn.UserID()] = conn
}

func (h *Hub) UnsubscribeFromChannel(conn *Connection, channelID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(conn.Subscriptions, channelID)

	if channelSubs, ok := h.channelSubs[channelID]; ok {
		delete(channelSubs, conn.UserID())
		if len(channelSubs) == 0 {
			delete(h.channelSubs, channelID)
		}
	}
}

// BroadcastToChannel delivers to everybody in a channel who is connected.
//
// The roster is read at send time. This used to go through a subscription table
// that nothing ever filled in — subscribeToUserChannels was a stub — so channel
// messages reached nobody and the only way to see a new one was to reload the
// page. Resolving membership per broadcast also means a channel somebody joins,
// or a conversation started with them, works immediately without anybody having
// to reconnect.
// Each broadcast is delivered to this process's own sockets and then published
// to the others, which deliver it through the same deliverTo* function. See
// fanout.go for why local delivery does not go through Redis, and how a
// process avoids acting on its own publication.
func (h *Hub) BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string) {
	h.deliverToChannel(channelID, msg, excludeUserID)
	h.publish(fanoutKindChannel, channelID, excludeUserID, msg)
}

// deliverToChannel resolves the channel's membership here and delivers to the
// local sockets it names.
//
// The receiving replicas run this too, on their own roster and their own
// explicit subscriptions. That is why an envelope carries the channel id and
// not a recipient list: membership resolved by whoever is about to deliver
// cannot disagree with itself, and somebody who joined a channel a moment ago
// is reached without the sending replica having to know.
func (h *Hub) deliverToChannel(channelID string, msg *protocol.Message, excludeUserID string) {
	recipients := map[string]bool{}

	if h.roster != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		members, err := h.roster.ChannelMemberIDs(ctx, channelID)
		if err != nil {
			h.logger.Warn().Err(err).Str("channel", channelID).Msg("could not resolve channel members for delivery")
		}
		for _, id := range members {
			recipients[id] = true
		}
	}

	// Anybody who explicitly subscribed is included too, so an explicit
	// subscription still works for clients that use one.
	h.mu.RLock()
	for userID := range h.channelSubs[channelID] {
		recipients[userID] = true
	}
	conns := make([]*Connection, 0, len(recipients))
	for userID := range recipients {
		if userID == excludeUserID {
			continue
		}
		for _, conn := range h.userConns[userID] {
			conns = append(conns, conn)
		}
	}
	h.mu.RUnlock()

	for _, conn := range conns {
		conn.Enqueue(msg)
	}
}

// BroadcastToUser delivers to every socket somebody has open.
//
// The connections are copied into a slice while the lock is held, as
// BroadcastToChannel does. This used to fetch the user's inner connection map,
// release the lock, and then range over that map — the map itself, not a copy
// — while Register and Unregister were free to write it. That is
// `fatal error: concurrent map iteration and map write`, a runtime throw
// gin.Recovery cannot catch, so one occurrence killed every session on the
// instance. Ringing somebody whose socket is opening or closing was enough to
// trigger it, which is what a page refresh during a call looks like.
func (h *Hub) BroadcastToUser(userID string, msg *protocol.Message) {
	h.deliverToUser(userID, msg)
	h.publish(fanoutKindUser, userID, "", msg)
}

func (h *Hub) deliverToUser(userID string, msg *protocol.Message) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.userConns[userID]))
	for _, conn := range h.userConns[userID] {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()

	for _, conn := range conns {
		conn.Enqueue(msg)
	}
}

// DisconnectUser closes every socket somebody has, on every replica, for when they
// have been deactivated. Otherwise a socket lives until the access token that opened
// it expires, and carries on being sent messages and asking for call tokens.
func (h *Hub) DisconnectUser(userID string) {
	h.closeUserConns(userID)
	// The fan-out drops a publication with no message as malformed, so this carries
	// one, though nothing is ever delivered to anybody.
	h.publish(fanoutKindDisconnect, userID, "", protocol.NewMessage(fanoutKindDisconnect, struct{}{}))
}

func (h *Hub) closeUserConns(userID string) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.userConns[userID]))
	for _, conn := range h.userConns[userID] {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()

	for _, conn := range conns {
		// As in cleanupStaleConnections: cancelling is what stops it, and the close
		// frame waits on the far end, which must not hold anybody else up.
		conn.cancel()
		go conn.finish()
		h.Unregister(conn)
	}
}

func (h *Hub) BroadcastToOrg(orgID string, msg *protocol.Message, excludeUserID string) {
	h.deliverToOrg(orgID, msg, excludeUserID)
	h.publish(fanoutKindOrg, orgID, excludeUserID, msg)
}

// deliverToOrg delivers to the local sockets belonging to one organization.
//
// The organization is checked against each connection rather than trusted from
// the address. Everything published goes to every process, so this check is
// what keeps one tenant's traffic — presence above all, which is the whole
// organization's business by definition — out of another tenant's sockets when
// it arrives over Redis.
func (h *Hub) deliverToOrg(orgID string, msg *protocol.Message, excludeUserID string) {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.userConns))
	for userID, userConns := range h.userConns {
		if userID == excludeUserID {
			continue
		}
		for _, conn := range userConns {
			if conn.OrgID() == orgID {
				conns = append(conns, conn)
			}
		}
	}
	h.mu.RUnlock()

	for _, conn := range conns {
		conn.Enqueue(msg)
	}
}

// broadcastPresence tells one organization that somebody's status changed.
//
// It used to tell every connected client on the server, whichever tenant they
// belonged to, leaking user ids and their online and offline timing across
// organizations. Nothing surfaced it because no browser could open a socket at
// all, so the loop never had anybody to send to.
//
// It goes through the ordinary organization broadcast, which means it crosses
// to the other replicas like everything else. Presence that did not would be
// worse than no presence: whether a colleague shows as online would depend on
// which replica each of the two of you happened to land on, and it would
// change under you on a reconnect. The list of who is online is read from
// Redis and was already shared; this is the event that says it changed.
func (h *Hub) broadcastPresence(orgID, userID, status, customStatus string) {
	h.BroadcastToOrg(orgID, protocol.NewMessage(protocol.TypePresenceChanged, protocol.PresenceChangedPayload{
		UserID:       userID,
		Status:       status,
		CustomStatus: customStatus,
	}), "")
}

// publish tells the other replicas about a broadcast that has just been
// delivered here. A no-op without a fan-out, which is a single-replica
// deployment.
func (h *Hub) publish(kind, target, exclude string, msg *protocol.Message) {
	if h.fanout == nil {
		return
	}
	h.fanout.publish(&fanoutEnvelope{
		Origin:  h.nodeID,
		Kind:    kind,
		Target:  target,
		Exclude: exclude,
		Msg:     msg,
	})
}

// deliverEnvelope delivers a broadcast that happened on another replica.
//
// An unknown kind is counted and ignored rather than guessed at. During a
// rolling upgrade the two versions are both publishing onto the same channel,
// and a kind this build does not understand is one it cannot address
// correctly — delivering it to the wrong people would be worse than the older
// replicas simply not carrying a new sort of message for the few minutes the
// rollout takes.
func (h *Hub) deliverEnvelope(env *fanoutEnvelope) {
	switch env.Kind {
	case fanoutKindChannel:
		h.deliverToChannel(env.Target, env.Msg, env.Exclude)
	case fanoutKindUser:
		h.deliverToUser(env.Target, env.Msg)
	case fanoutKindOrg:
		h.deliverToOrg(env.Target, env.Msg, env.Exclude)
	case fanoutKindDisconnect:
		h.closeUserConns(env.Target)
	default:
		wsFanoutDropped.WithLabelValues("unknown_kind").Inc()
		h.logger.Warn().Str("kind", env.Kind).
			Msg("fan-out: a replica published a kind this build does not know")
	}
}

// ResyncAfterGap ends every session this process is serving, after it was cut
// off from the other replicas for an unknown length of time.
//
// Redis pub/sub does not replay. When a subscription comes back there is no
// way to learn what was published while it was gone, and no way to tell the
// difference between nothing and everything. The clients here are still
// connected and still believe they are current — the one state this whole
// change exists to prevent, because it is invisible from both ends.
//
// So they are disconnected. The client reconnects and refetches, which is the
// same recovery already relied on for a client that falls too far behind, and
// the messages themselves were never at risk: they are in PostgreSQL. It is a
// blunt instrument and it costs a reconnect storm proportional to one
// replica's share of the users, which at this product's size is tens of
// sockets. The alternative is to say nothing and let people miss messages they
// will never know existed.
func (h *Hub) ResyncAfterGap() {
	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.connections))
	for _, conn := range h.connections {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()

	if len(conns) == 0 {
		return
	}

	h.logger.Warn().Int("sessions", len(conns)).
		Msg("closing sessions after a fan-out gap: reconnecting is what refetches whatever was missed")

	for _, conn := range conns {
		// Cancel first so nothing else offers to them, and close in the
		// background: the close handshake waits on the peer, and one slow
		// client must not hold up the rest. Unregistering is left to the
		// handler that owns the session, as it is everywhere else that a
		// connection is closed from outside its own loops.
		conn.cancel()
		go conn.finish()
	}
}

// cleanupStaleConnections closes and unregisters connections that have stopped
// answering.
//
// It reaps only what this process owns, and it can only reap what this process
// owns: h.connections holds the sockets terminated here and nothing else.
// That is the rule with several replicas writing the same presence — a process
// may retire its own sessions and no others. The sessions of a process that
// died without unregistering are not this one's to remove either; they carry a
// deadline their owner was extending on every heartbeat, and stop counting on
// their own once it stops.
//
// This used to range over the connections, assign each to _, and return, so the
// 30-second timer fired and accomplished nothing. A half-open connection stayed
// in the hub maps for ever: its goroutines and 256-slot buffer were never
// reclaimed, every broadcast walked it, and — because SetOffline only runs from
// Unregister — the user was shown online to their whole organisation until the
// process restarted.
//
// The stale connections are collected under the lock and dealt with after it is
// released: closing writes a close frame to the network and Unregister takes
// the write lock itself, neither of which can happen here.
func (h *Hub) cleanupStaleConnections() {
	cutoff := time.Now().Add(-staleAfter).UnixNano()

	h.mu.RLock()
	var stale []*Connection
	for _, conn := range h.connections {
		if conn.lastSeen.Load() < cutoff {
			stale = append(stale, conn)
		}
	}
	h.mu.RUnlock()

	for _, conn := range stale {
		h.logger.Info().
			Str("conn", conn.ID).
			Str("user", conn.UserID()).
			Msg("reaping connection: no frame since the liveness deadline")

		// Cancel now — that is what stops the loops — and let the close frame
		// go out in the background. Closing waits for the peer to answer the
		// close handshake, and the peer here is by definition not answering, so
		// it blocks until the library's own timeout. Done inline, one dead
		// connection would hold up the reaping of every other one behind it on
		// this single goroutine.
		conn.cancel()
		go conn.finish()

		h.Unregister(conn)
	}
}

// finish ends a connection once, whichever loop notices first.
//
// Cancelling the context is what stops the writer, and it is deliberately the
// only thing that does. This used to close SendCh here, in the reader, while
// every broadcast path was still free to enqueue to the same connection — so a
// send on a closed channel panicked from a goroutine with no recover and took
// the process with it. The channel now has no closer at all: it is garbage
// collected with the connection, and Enqueue stops offering to it as soon as
// the context is done.
// A connection with no socket is still finished properly. finish is now
// reached from the broadcast path too — a client too far behind is closed there
// — and that runs on goroutines with no recover, where a nil dereference is
// fatal to the process rather than to one request. Cancelling is the part that
// matters; there is simply nothing to send a close frame to.
func (c *Connection) finish() {
	c.CloseOnce.Do(func() {
		c.cancel()
		if c.WS != nil {
			c.WS.Close(websocket.StatusNormalClosure, "")
		}
	})
}

func (c *Connection) ReadLoop() {
	defer c.finish()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			_, data, err := c.WS.Read(c.ctx)
			if err != nil {
				c.logger.Debug().Err(err).Str("conn", c.ID).Msg("WS read error")
				return
			}

			// Anything at all arriving proves the client is still there,
			// including a frame this server cannot parse.
			c.touch()

			now := time.Now()
			if !c.frames.take(now) {
				if c.frames.floods() {
					c.logger.Warn().Str("conn", c.ID).Str("user", c.userID).Msg("closing a socket that kept sending past its limit")
					_ = c.WS.Close(websocket.StatusPolicyViolation, "too many messages")
					return
				}
				continue
			}

			var msg protocol.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				c.logger.Warn().Err(err).Str("conn", c.ID).Msg("Invalid WS message")
				continue
			}
			if msg.Type == protocol.TypeChatTyping && c.typingTooSoon(&msg, now) {
				continue
			}

			c.handleMessage(&msg)
		}
	}
}

// serverFeatures names what this build can be asked to do, so a client can
// tell an older server from a newer one without probing it.
var serverFeatures = []string{"chat", "presence", "calls", "notifications"}

// acknowledge completes the handshake.
//
// The client opens the socket, sends conn.init and waits in its 'connecting'
// state; everything handed to send() queues until a conn.ack arrives. Nothing
// in the server ever sent one — handleMessage had no case for conn.init, so it
// fell through to the unknown-message default — so the queue never drained and
// no client-to-server frame the app produced was ever sent.
//
// The identity in the ack is the server's, taken from the claims that opened
// the socket. The token in ConnInitPayload is deliberately ignored: the
// handshake already authenticated from the cookie, and honouring a token
// supplied over an established socket would let a client rename itself to
// somebody else after connecting.
func (c *Connection) acknowledge() {
	c.Enqueue(protocol.NewMessage(protocol.TypeConnAck, protocol.ConnAckPayload{
		ConnectionID: c.ID,
		UserID:       c.userID,
		OrgID:        c.orgID,
		ServerTime:   time.Now().UTC().Format(time.RFC3339),
		Features:     serverFeatures,
	}))
}

func (c *Connection) handleMessage(msg *protocol.Message) {
	ctx := c.ctx

	switch msg.Type {
	case protocol.TypeConnInit:
		c.acknowledge()
	case protocol.TypeConnPong:
		// The liveness bookkeeping already happened in ReadLoop, which counts
		// every inbound frame. The case exists so that a client answering the
		// ping correctly is not logged as an unknown message type.

	case protocol.TypeChatSend:
		c.hub.services.HandleSendMessage(ctx, c, msg)
	case protocol.TypeChatEdit:
		c.hub.services.HandleEditMessage(ctx, c, msg)
	case protocol.TypeChatDelete:
		c.hub.services.HandleDeleteMessage(ctx, c, msg)
	case protocol.TypeChatTyping:
		c.hub.services.HandleTyping(ctx, c, msg)
	case protocol.TypeChatRead:
		c.hub.services.HandleRead(ctx, c, msg)

	case protocol.TypePresenceUpdate:
		c.hub.presenceService.HandlePresenceUpdate(ctx, c, msg)

	case protocol.TypeCallInvite:
		c.hub.services.HandleCallInvite(ctx, c, msg)
	case protocol.TypeCallAccept:
		c.hub.services.HandleCallAccept(ctx, c, msg)
	case protocol.TypeCallDecline:
		c.hub.services.HandleCallDecline(ctx, c, msg)
	case protocol.TypeCallSignaling:
		c.hub.services.HandleCallSignaling(ctx, c, msg)

	default:
		c.logger.Debug().Str("type", msg.Type).Msg("Unknown message type")
	}
}

func (c *Connection) WriteLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case msg := <-c.SendCh:
			if err := c.writeJSON(c.ctx, msg); err != nil {
				c.logger.Debug().Err(err).Str("conn", c.ID).Msg("WS write error")
				return
			}
		case <-ticker.C:
			if err := c.heartbeat(); err != nil {
				return
			}
		}
	}
}

// touch records that the client has just proved it is still there.
func (c *Connection) touch() {
	c.lastSeen.Store(time.Now().UnixNano())
}

// heartbeat asks the client to prove it is still there, and keeps its presence
// entry from expiring while it does.
//
// The two belong together. The presence keys carry a TTL so that a process
// which is killed or OOMs — and so never runs Unregister — stops claiming
// everybody it served is online; the price of that TTL is that somebody who is
// still connected has to be refreshed, and this is the only thing that already
// runs periodically per connection.
func (c *Connection) heartbeat() error {
	if refresher, ok := c.hub.presenceService.(PresenceRefresher); ok {
		if err := refresher.Touch(c.userID, c.orgID); err != nil {
			c.logger.Debug().Err(err).Str("conn", c.ID).Msg("could not refresh presence")
		}
	}
	// The session claim expires on the same schedule and for the same reason:
	// it is what stops a process that was killed from holding this person open
	// as connected for ever, on every other replica, with nobody left to
	// correct it.
	if c.hub.sessions != nil {
		if err := c.hub.sessions.TouchSession(c.userID, c.hub.sessionID(c)); err != nil {
			c.logger.Debug().Err(err).Str("conn", c.ID).Msg("could not refresh the session claim")
		}
	}
	return c.writeJSON(c.ctx, protocol.NewMessage(protocol.TypeConnPing, map[string]interface{}{}))
}

func (c *Connection) writeJSON(ctx context.Context, msg *protocol.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.WS.Write(ctx, websocket.MessageText, data)
}

// WSHandler upgrades HTTP to WebSocket.
//
// allowedOrigins are extra hosts permitted to open a socket, for deployments
// where the app is served from somewhere other than this server. Same-origin
// is always allowed and is the normal case.
func WSHandler(hub *Hub, authService AuthServiceInterface, allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The access token lives in an HttpOnly cookie, so a browser cannot
		// read it to put in the query string — which is why the socket never
		// connected from the app at all, taking presence, live messages and
		// call ringing down with it. Prefer the cookie the browser already
		// sends; other clients send a bearer header. A token in the query
		// string is not accepted: addresses end up in logs.
		token := ""
		if cookie, err := c.Request.Cookie("at"); err == nil {
			token = cookie.Value
		}
		if token == "" {
			if header := c.GetHeader("Authorization"); strings.HasPrefix(header, "Bearer ") {
				token = strings.TrimPrefix(header, "Bearer ")
			}
		}
		if token == "" {
			c.JSON(401, gin.H{"error": "missing token"})
			return
		}

		claims, err := authService.ValidateAccessToken(c.Request.Context(), token)
		if err != nil {
			c.JSON(401, gin.H{"error": "invalid token"})
			return
		}
		// Somebody on a password an administrator set does nothing until they
		// choose their own; the HTTP API refuses them, and so does this, or
		// whoever set the password could post in their name over the socket.
		if claims.MustChangePassword {
			c.JSON(403, gin.H{"error": "set your own password before continuing", "mustChangePassword": true})
			return
		}

		// The handshake authenticates with a cookie the browser attaches by
		// itself, so without an Origin check any site could open a socket as
		// whoever is logged in. Accept checks same-origin by default; skipping
		// it left SameSite on the cookie as the only thing in the way.
		if hub.socketsOf(claims.UserID) >= maxSocketsPerUser {
			c.JSON(429, gin.H{"error": "too many open connections; close some tabs"})
			return
		}

		wsConn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
			OriginPatterns: allowedOrigins,
		})
		if err != nil {
			hub.logger.Error().Err(err).Msg("WS upgrade failed")
			return
		}
		wsConn.SetReadLimit(maxFrameBytes)

		ctx, cancel := context.WithCancel(c.Request.Context())
		conn := &Connection{
			ID:            generateConnectionID(),
			userID:        claims.UserID,
			orgID:         claims.OrgID,
			WS:            wsConn,
			SendCh:        make(chan *protocol.Message, 256),
			Subscriptions: make(map[string]bool),
			ctx:           ctx,
			cancel:        cancel,
			hub:           hub,
			logger:        hub.logger,
		}

		// A socket must not outlive the token that opened it: it is checked
		// once here and the connection then lives for hours, so a revoked or
		// expired session would keep receiving messages.
		if !claims.ExpiresAt.IsZero() {
			if remaining := time.Until(claims.ExpiresAt); remaining > 0 {
				expiry := time.AfterFunc(remaining, func() {
					conn.logger.Debug().Str("user", conn.UserID()).Msg("closing socket: access token expired")
					conn.cancel()
					_ = wsConn.Close(websocket.StatusPolicyViolation, "token expired")
				})
				// A socket closed sooner must not stay referenced until then.
				defer expiry.Stop()
			} else {
				_ = wsConn.Close(websocket.StatusPolicyViolation, "token expired")
				cancel()
				return
			}
		}

		hub.Register(conn)

		go conn.ReadLoop()
		go conn.WriteLoop()

		<-ctx.Done()
		hub.Unregister(conn)
	}
}

func generateConnectionID() string {
	return fmt.Sprintf("conn-%d-%x", time.Now().UnixNano(), rand.Uint32())
}
