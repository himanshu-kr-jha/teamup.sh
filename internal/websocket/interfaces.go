package websocket

import (
	"context"
	"time"

	"github.com/teamup/teamup/pkg/protocol"
)

// WSConnection defines the interface for a WebSocket connection
type WSConnection interface {
	UserID() string
	OrgID() string
	Enqueue(msg *protocol.Message)
}

// ServiceInterface defines the interface for services that the hub interacts with
type ServiceInterface interface {
	// Chat
	HandleSendMessage(ctx context.Context, conn WSConnection, msg *protocol.Message)
	HandleEditMessage(ctx context.Context, conn WSConnection, msg *protocol.Message)
	HandleDeleteMessage(ctx context.Context, conn WSConnection, msg *protocol.Message)
	HandleTyping(ctx context.Context, conn WSConnection, msg *protocol.Message)
	HandleRead(ctx context.Context, conn WSConnection, msg *protocol.Message)

	// Calls
	HandleCallInvite(ctx context.Context, conn WSConnection, msg *protocol.Message)
	HandleCallAccept(ctx context.Context, conn WSConnection, msg *protocol.Message)
	HandleCallDecline(ctx context.Context, conn WSConnection, msg *protocol.Message)
	HandleCallSignaling(ctx context.Context, conn WSConnection, msg *protocol.Message)
}

// PresenceServiceInterface defines the interface for presence service
type PresenceServiceInterface interface {
	SetOnline(userID, orgID string) error
	SetOffline(userID, orgID string) error
	UpdateStatus(userID, orgID string, status, customStatus, customEmoji string) error
	HandlePresenceUpdate(ctx context.Context, conn WSConnection, msg *protocol.Message)
	GetPresence(userID, orgID string) (*Presence, error)
	GetAllOnline(orgID string) ([]Presence, error)
}

// AuthServiceInterface defines the interface for auth service
type AuthServiceInterface interface {
	ValidateAccessToken(ctx context.Context, token string) (*Claims, error)
}

// Claims represents the JWT claims
type Claims struct {
	UserID             string
	OrgID              string
	Email              string
	Roles              []string
	SessionID          string
	MustChangePassword bool
	AuthProvider       string
	IDToken            string
}

// Presence represents user presence
// Serialised onto the wire by the presence endpoints, so the tags have to
// match what the frontend reads — without them every status came back as
// PascalCase and no presence indicator ever lit up.
type Presence struct {
	UserID       string    `json:"userId"`
	OrgID        string    `json:"orgId"`
	Status       string    `json:"status"`
	CustomStatus string    `json:"customStatus,omitempty"`
	CustomEmoji  string    `json:"customEmoji,omitempty"`
	LastActivity time.Time `json:"lastActivity"`
}
