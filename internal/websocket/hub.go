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

	"github.com/teamup/teamup/pkg/protocol"
)

type Hub struct {
	services        ServiceInterface
	presenceService PresenceServiceInterface
	authService     AuthServiceInterface
	roster          ChannelRosterInterface
	calls           CallInviteDeliverer
	logger          zerolog.Logger

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
	return &Hub{
		services:        services,
		presenceService: presenceService,
		authService:     authService,
		logger:          logger,
		connections:     make(map[string]*Connection),
		userConns:       make(map[string]map[string]*Connection),
		channelSubs:     make(map[string]map[string]*Connection),
		stop:            make(chan struct{}),
	}
}

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
	h.connections[conn.ID] = conn

	if h.userConns[conn.UserID()] == nil {
		h.userConns[conn.UserID()] = make(map[string]*Connection)
	}
	h.userConns[conn.UserID()][conn.ID] = conn

	h.mu.Unlock()

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

// Unregister removes a connection, announcing the user as offline once their
// last one has gone. As in Register, the announcement is made after the lock is
// released.
func (h *Hub) Unregister(conn *Connection) {
	h.mu.Lock()
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

	if wentOffline {
		h.presenceService.SetOffline(conn.UserID(), conn.OrgID())
		h.broadcastPresence(conn.OrgID(), conn.UserID(), "offline", "")
	}
}

// SetCallInviteDeliverer wires the check for calls waiting on somebody.
func (h *Hub) SetCallInviteDeliverer(c CallInviteDeliverer) { h.calls = c }

// IsConnected reports whether somebody has a session open, which is what
// separates a phone that is ringing from one that is merely being called.
func (h *Hub) IsConnected(userID string) bool {
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
func (h *Hub) BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string) {
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

func (h *Hub) BroadcastToOrg(orgID string, msg *protocol.Message, excludeUserID string) {
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
func (h *Hub) broadcastPresence(orgID, userID, status, customStatus string) {
	msg := protocol.NewMessage(protocol.TypePresenceChanged, protocol.PresenceChangedPayload{
		UserID:       userID,
		Status:       status,
		CustomStatus: customStatus,
	})

	h.mu.RLock()
	conns := make([]*Connection, 0, len(h.userConns))
	for _, userConns := range h.userConns {
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

// cleanupStaleConnections closes and unregisters connections that have stopped
// answering.
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

			var msg protocol.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				c.logger.Warn().Err(err).Str("conn", c.ID).Msg("Invalid WS message")
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
		// sends; the query parameter stays for non-browser clients.
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
			token = c.Query("token")
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

		// The handshake authenticates with a cookie the browser attaches by
		// itself, so without an Origin check any site could open a socket as
		// whoever is logged in. Accept checks same-origin by default; skipping
		// it left SameSite on the cookie as the only thing in the way.
		wsConn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
			OriginPatterns: allowedOrigins,
		})
		if err != nil {
			hub.logger.Error().Err(err).Msg("WS upgrade failed")
			return
		}

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
				time.AfterFunc(remaining, func() {
					conn.logger.Debug().Str("user", conn.UserID()).Msg("closing socket: access token expired")
					conn.cancel()
					_ = wsConn.Close(websocket.StatusPolicyViolation, "token expired")
				})
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
