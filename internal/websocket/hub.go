package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
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

func (h *Hub) Register(conn *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.connections[conn.ID] = conn

	if h.userConns[conn.UserID()] == nil {
		h.userConns[conn.UserID()] = make(map[string]*Connection)
	}
	h.userConns[conn.UserID()][conn.ID] = conn

	h.subscribeToUserChannels(conn)

	h.presenceService.SetOnline(conn.UserID(), conn.OrgID())
	h.broadcastPresence(conn.UserID(), "online", "")
}

func (h *Hub) Unregister(conn *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.connections, conn.ID)

	if userConns, ok := h.userConns[conn.UserID()]; ok {
		delete(userConns, conn.ID)
		if len(userConns) == 0 {
			delete(h.userConns, conn.UserID())
			h.presenceService.SetOffline(conn.UserID(), conn.OrgID())
			h.broadcastPresence(conn.UserID(), "offline", "")
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
}

func (h *Hub) subscribeToUserChannels(conn *Connection) {
	// In a real implementation, fetch user's channels from chat service
}

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

func (h *Hub) BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string) {
	h.mu.RLock()
	channelSubs, ok := h.channelSubs[channelID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	for userID, conn := range channelSubs {
		if userID == excludeUserID {
			continue
		}
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

func (h *Hub) broadcastPresence(userID, status, customStatus string) {
	msg := protocol.NewMessage(protocol.TypePresenceChanged, protocol.PresenceChangedPayload{
		UserID:       userID,
		Status:       status,
		CustomStatus: customStatus,
	})

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, userConns := range h.userConns {
		for _, conn := range userConns {
			conn.Enqueue(msg)
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

// WSHandler upgrades HTTP to WebSocket
func WSHandler(hub *Hub, authService AuthServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("token")
		if token == "" {
			c.JSON(401, gin.H{"error": "missing token"})
			return
		}

		claims, err := authService.ValidateAccessToken(c.Request.Context(), token)
		if err != nil {
			c.JSON(401, gin.H{"error": "invalid token"})
			return
		}

		wsConn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
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
