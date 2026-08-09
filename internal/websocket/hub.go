package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"math/rand"
	"strings"
	"sync"
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
}

func (c *Connection) UserID() string {
	return c.userID
}

func (c *Connection) OrgID() string {
	return c.orgID
}

func (c *Connection) Enqueue(msg *protocol.Message) {
	select {
	case c.SendCh <- msg:
	default:
		// Buffer full - client is slow
	}
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
	}
}

func (h *Hub) Run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.cleanupStaleConnections()
		}
	}
}

// Register adds a connection and announces the user as online.
//
// The announcement happens after the lock is released. sync.RWMutex is not
// reentrant, so calling broadcastPresence — which takes a read lock — while
// still holding the write lock deadlocked the hub permanently on the very
// first connection, taking presence, live messages and call ringing with it.
func (h *Hub) Register(conn *Connection) {
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

func (h *Hub) BroadcastToUser(userID string, msg *protocol.Message) {
	h.mu.RLock()
	userConns, ok := h.userConns[userID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	for _, conn := range userConns {
		conn.Enqueue(msg)
	}
}

func (h *Hub) BroadcastToOrg(orgID string, msg *protocol.Message, excludeUserID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for userID, userConns := range h.userConns {
		if userID == excludeUserID {
			continue
		}
		for _, conn := range userConns {
			if conn.OrgID() == orgID {
				conn.Enqueue(msg)
			}
		}
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
	defer h.mu.RUnlock()

	for _, userConns := range h.userConns {
		for _, conn := range userConns {
			if conn.OrgID() == orgID {
				conn.Enqueue(msg)
			}
		}
	}
}

func (h *Hub) cleanupStaleConnections() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, conn := range h.connections {
		_ = conn
		// Check if connection is still alive
	}
}

func (c *Connection) ReadLoop() {
	defer func() {
		c.CloseOnce.Do(func() {
			c.cancel()
			close(c.SendCh)
			c.WS.Close(websocket.StatusNormalClosure, "")
		})
	}()

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

			var msg protocol.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				c.logger.Warn().Err(err).Str("conn", c.ID).Msg("Invalid WS message")
				continue
			}

			c.handleMessage(&msg)
		}
	}
}

func (c *Connection) handleMessage(msg *protocol.Message) {
	ctx := c.ctx

	switch msg.Type {
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
		case msg, ok := <-c.SendCh:
			if !ok {
				return
			}
			if err := c.writeJSON(c.ctx, msg); err != nil {
				c.logger.Debug().Err(err).Str("conn", c.ID).Msg("WS write error")
				return
			}
		case <-ticker.C:
			pingMsg := protocol.NewMessage(protocol.TypeConnPing, map[string]interface{}{})
			if err := c.writeJSON(c.ctx, pingMsg); err != nil {
				return
			}
		}
	}
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
