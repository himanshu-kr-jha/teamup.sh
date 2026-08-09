package websocket

import (
	"context"
	"github.com/google/uuid"
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

// ChannelRosterInterface answers who is in a channel. Delivery resolves this
// at send time rather than keeping a subscription table in step with every
// channel somebody joins, leaves or has created for them while connected.
type ChannelRosterInterface interface {
	ChannelMemberIDs(ctx context.Context, channelID string) ([]string, error)
}

// CallInviteDeliverer rings somebody for any call still waiting on them.
// Connecting is the moment to do it: whoever called may have been waiting the
// whole time they were away.
type CallInviteDeliverer interface {
	DeliverPendingInvitations(ctx context.Context, userID uuid.UUID)
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
	// ExpiresAt is when the access token stops being valid. A socket is
	// authenticated once at the handshake and then lives for hours, so it has
	// to be closed at this point rather than outliving the token.
	ExpiresAt    time.Time
	AuthProvider string
	IDToken      string
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
