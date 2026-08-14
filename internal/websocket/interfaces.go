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

// PresenceRefresher extends the TTL on a connected user's presence entry.
//
// Deliberately separate from PresenceServiceInterface rather than another
// method on it: that interface is implemented by test doubles in several
// packages, and a presence store with no expiry — anything backed by something
// other than Redis — has nothing to refresh. The hub asks for it and carries on
// without it.
type PresenceRefresher interface {
	Touch(userID, orgID string) error
}

// PresenceSessions counts a person's live sockets across every process.
//
// The hub's maps only ever hold this process's connections, which makes two
// questions unanswerable from them once there is more than one replica: is
// this person reachable at all, and was that their last socket. Getting the
// second one wrong is the damaging half — a process that assumes so publishes
// somebody as offline while they are sitting in another replica's map, and
// their colleagues watch them disappear.
//
// Separate from PresenceServiceInterface for the same reason PresenceRefresher
// is: the interface is implemented by test doubles in several packages, and a
// presence store that is not shared between processes has nothing to count.
// The hub asks for it and manages without.
//
// A session id names one socket across the cluster; the hub builds it from its
// own node id, so an implementation only has to store and count strings.
// Nothing is scoped by organization here because the question arrives without
// one at every call site that asks it — the caller has a user id and wants to
// know whether to send a push — and a user belongs to exactly one
// organization, so the id needs no further qualification. Who a message is
// then delivered to remains organization-checked in the hub.
type PresenceSessions interface {
	// AddSession records a socket that has just opened.
	AddSession(userID, sessionID string) error
	// TouchSession keeps a live socket's claim from expiring.
	TouchSession(userID, sessionID string) error
	// RemoveSession gives up a socket and reports how many the person has left
	// anywhere in the cluster.
	RemoveSession(userID, sessionID string) (remaining int, err error)
	// AnySession reports whether the person has a socket open anywhere.
	AnySession(userID string) (bool, error)
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
