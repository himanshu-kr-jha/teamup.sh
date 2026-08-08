package protocol

import (
	"fmt"
	"time"
)

// Message types
const (
	// Connection
	TypeConnInit  = "conn.init"
	TypeConnAck   = "conn.ack"
	TypeConnPing  = "conn.ping"
	TypeConnPong  = "conn.pong"
	TypeConnClose = "conn.close"

	// Auth
	TypeAuthRefresh   = "auth.refresh"
	TypeAuthRefreshed = "auth.refreshed"

	// Chat
	TypeChatSend       = "chat.send"
	TypeChatSent       = "chat.sent"
	TypeChatMessage    = "chat.message"
	TypeChatEdit       = "chat.edit"
	TypeChatDelete     = "chat.delete"
	TypeChatReaction   = "chat.reaction"
	TypeChatTyping     = "chat.typing"
	TypeChatRead       = "chat.read"
	TypeChatMembership = "chat.membership.changed"

	// Presence
	TypePresenceUpdate  = "presence.update"
	TypePresenceChanged = "presence.changed"
	TypePresenceSync    = "presence.sync"

	// Calls
	TypeCallInvite         = "call.invite"
	TypeCallInvited        = "call.invited"
	TypeCallAccept         = "call.accept"
	TypeCallDecline        = "call.decline"
	TypeCallEnded          = "call.ended"
	TypeCallSignaling      = "call.signaling"
	TypeCallSignalingRelay = "call.signaling.relay"

	// Notifications
	TypeNotificationNew  = "notification.new"
	TypeNotificationRead = "notification.read"
)

// Message is the base WebSocket message envelope
type Message struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
	Version   int         `json:"version"`
}

// Connection payloads
type ConnInitPayload struct {
	Token      string     `json:"token"`
	ClientInfo ClientInfo `json:"clientInfo"`
}

type ClientInfo struct {
	Platform string `json:"platform"`
	Version  string `json:"version"`
}

type ConnAckPayload struct {
	ConnectionID string   `json:"connectionId"`
	ServerTime   string   `json:"serverTime"`
	Features     []string `json:"features"`
}

type ConnClosePayload struct {
	Reason string `json:"reason"`
	Code   int    `json:"code"`
}

// Auth payloads
type AuthRefreshPayload struct {
	RefreshToken string `json:"refreshToken"`
}

type AuthRefreshedPayload struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int    `json:"expiresIn"`
}

// Chat payloads
type ChatSendPayload struct {
	ChannelID   string       `json:"channelId"`
	Content     string       `json:"content"`
	ReplyTo     *string      `json:"replyTo,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	TempID      string       `json:"tempId"` // Client-generated for optimistic updates
}

type ChatSentPayload struct {
	TempID    string    `json:"tempId"`
	MessageID string    `json:"messageId"`
	Timestamp time.Time `json:"timestamp"`
}

type ChatMessagePayload struct {
	Message MessageData `json:"message"`
}

type MessageData struct {
	ID          string              `json:"id"`
	ChannelID   string              `json:"channelId"`
	UserID      string              `json:"userId"`
	UserName    string              `json:"userName"`
	UserAvatar  string              `json:"userAvatar,omitempty"`
	Content     string              `json:"content"`
	Attachments []Attachment        `json:"attachments,omitempty"`
	ReplyTo     *string             `json:"replyTo,omitempty"`
	Reactions   map[string][]string `json:"reactions,omitempty"` // emoji -> userIDs
	CreatedAt   time.Time           `json:"createdAt"`
	EditedAt    *time.Time          `json:"editedAt,omitempty"`
	DeletedAt   *time.Time          `json:"deletedAt,omitempty"`
	Pending     bool                `json:"pending,omitempty"` // For optimistic UI
}

type Attachment struct {
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType"`
	SizeBytes    int64  `json:"sizeBytes"`
	URL          string `json:"url,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	ObjectKey    string `json:"objectKey"`
}

type ChatEditPayload struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content"`
}

type ChatDeletePayload struct {
	MessageID string `json:"messageId"`
}

type ChatReactionPayload struct {
	MessageID string `json:"messageId"`
	Emoji     string `json:"emoji"`
	Action    string `json:"action"` // "add" | "remove"
}

type ChatTypingPayload struct {
	ChannelID string `json:"channelId"`
	IsTyping  bool   `json:"isTyping"`
}

type ChatReadPayload struct {
	ChannelID string `json:"channelId"`
	MessageID string `json:"messageId"`
}

type ChatMembershipChangedPayload struct {
	ChannelID string   `json:"channelId"`
	UserIDs   []string `json:"userIds"`
	Action    string   `json:"action"` // "added" | "removed"
}

// Presence payloads
type PresenceUpdatePayload struct {
	Status            string `json:"status"` // "online" | "away" | "busy" | "in_call" | "offline"
	CustomStatus      string `json:"customStatus,omitempty"`
	CustomStatusEmoji string `json:"customStatusEmoji,omitempty"`
}

type PresenceChangedPayload struct {
	UserID            string     `json:"userId"`
	Status            string     `json:"status"`
	CustomStatus      string     `json:"customStatus,omitempty"`
	CustomStatusEmoji string     `json:"customStatusEmoji,omitempty"`
	LastSeen          *time.Time `json:"lastSeen,omitempty"`
}

type PresenceSyncPayload struct {
	Users []UserPresence `json:"users"`
}

type UserPresence struct {
	UserID            string     `json:"userId"`
	UserName          string     `json:"userName"`
	UserAvatar        string     `json:"userAvatar,omitempty"`
	Status            string     `json:"status"`
	CustomStatus      string     `json:"customStatus,omitempty"`
	CustomStatusEmoji string     `json:"customStatusEmoji,omitempty"`
	LastSeen          *time.Time `json:"lastSeen,omitempty"`
}

// Call payloads
type CallInvitePayload struct {
	RoomName string   `json:"roomName"`
	Invitees []string `json:"invitees"`
	CallType string   `json:"callType"` // "video" | "audio"
	Title    string   `json:"title,omitempty"`
}

type CallInvitedPayload struct {
	RoomName   string `json:"roomName"`
	CallerID   string `json:"callerId"`
	CallerName string `json:"callerName"`
	CallType   string `json:"callType"`
	Token      string `json:"token"` // LiveKit token
}

type CallAcceptPayload struct {
	RoomName string `json:"roomName"`
}

type CallDeclinePayload struct {
	RoomName string `json:"roomName"`
	Reason   string `json:"reason,omitempty"`
}

type CallEndedPayload struct {
	RoomName string `json:"roomName"`
	Duration int    `json:"duration"` // seconds
}

type CallSignalingPayload struct {
	RoomName          string           `json:"roomName"`
	TargetParticipant string           `json:"targetParticipant"`
	Payload           SignalingPayload `json:"payload"`
}

type CallSignalingRelayPayload struct {
	RoomName        string           `json:"roomName"`
	FromParticipant string           `json:"fromParticipant"`
	Payload         SignalingPayload `json:"payload"`
}

type SignalingPayload struct {
	Type          string `json:"type"` // "offer" | "answer" | "ice-candidate" | "renegotiate"
	SDP           string `json:"sdp,omitempty"`
	Candidate     string `json:"candidate,omitempty"`
	SDPMid        string `json:"sdpMid,omitempty"`
	SDPMLineIndex int    `json:"sdpMLineIndex,omitempty"`
}

// Notification payloads
type NotificationNewPayload struct {
	Notification NotificationData `json:"notification"`
}

type NotificationData struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // "chat.message" | "call.invite" | "task.assigned" | "mention" | "task.due"
	Title     string                 `json:"title"`
	Body      string                 `json:"body"`
	Data      map[string]interface{} `json:"data,omitempty"`
	ReadAt    *time.Time             `json:"readAt,omitempty"`
	CreatedAt time.Time              `json:"createdAt"`
}

type NotificationReadPayload struct {
	NotificationID string `json:"notificationId"`
}

// Outgoing message helper
func NewMessage(msgType string, payload interface{}) *Message {
	return &Message{
		ID:        generateID(),
		Type:      msgType,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
		Version:   1,
	}
}

func generateID() string {
	// Simple UUID-like generation
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		randUint32(), randUint16(), randUint16(), randUint16(), randUint48())
}

// Minimal random for ID generation (not crypto-secure, just for message IDs)
func randUint32() uint32 { return uint32(time.Now().UnixNano()) }
func randUint16() uint16 { return uint16(time.Now().UnixNano() >> 16) }
func randUint48() uint64 { return uint64(time.Now().UnixNano()) & 0xFFFFFFFFFFFF }
