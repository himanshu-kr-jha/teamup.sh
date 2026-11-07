package protocol

import (
	"encoding/json"
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
	TypeChatSend     = "chat.send"
	TypeChatSent     = "chat.sent"
	TypeChatMessage  = "chat.message"
	TypeChatEdit     = "chat.edit"
	TypeChatDelete   = "chat.delete"
	TypeChatReaction = "chat.reaction"
	TypeChatTyping   = "chat.typing"
	TypeChatRead     = "chat.read"
	// TypeChatReadReceipt travels the other way: somebody has read up to a
	// point, and the people whose messages that covers are told so their tick
	// can change while they are looking at it.
	TypeChatReadReceipt = "chat.read.receipt"
	TypeChatMembership  = "chat.membership.changed"

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
	// Somebody is at the door of a private call.
	TypeCallKnock = "call.knock"
	// The answer, to whoever knocked.
	TypeCallKnockResult = "call.knock.result"
	// Somebody else answered, so another member's prompt can go away.
	TypeCallKnockResolved = "call.knock.resolved"

	// Notifications
	TypeNotificationNew  = "notification.new"
	TypeNotificationRead = "notification.read"
)

// Message is the base WebSocket message envelope.
//
// One struct serves both directions, so the payload field has to be readable
// coming in as well as writable going out. It used to be an interface{}, which
// encoding/json fills with map[string]interface{} on the way in — never with
// the []byte every inbound handler asserted it to. Each of those assertions
// failed, so the handler returned before doing anything and every
// client-to-server frame was discarded without a log line. Raw JSON decodes
// straight into a handler's own payload struct and re-marshals unchanged, so
// the wire format is the same in both directions.
type Message struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
	Version   int             `json:"version"`
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

// ConnAckPayload answers conn.init and is what moves a client from
// 'connecting' to 'ready'. It carries the identity the server settled on
// rather than leaving the client to trust what it sent: the socket is
// authenticated from a cookie at the handshake, so who the connection actually
// belongs to is the server's answer to give, and a client that queued work
// against the wrong account would send it to the wrong place.
type ConnAckPayload struct {
	ConnectionID string   `json:"connectionId"`
	UserID       string   `json:"userId"`
	OrgID        string   `json:"orgId"`
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
	// Kind is "user" for something somebody wrote and "event" for something
	// that happened, like a missed call.
	Kind      string     `json:"kind,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	EditedAt  *time.Time `json:"editedAt,omitempty"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
	Pending   bool       `json:"pending,omitempty"` // For optimistic UI
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
	// UserID names who reacted. Inbound it is implied by the connection and
	// left empty; outbound it is the point, because a client cannot show a
	// reaction, or decide whether it is its own, without knowing whose it is.
	UserID string `json:"userId,omitempty"`
}

type ChatTypingPayload struct {
	ChannelID string `json:"channelId"`
	IsTyping  bool   `json:"isTyping"`
}

type ChatReadPayload struct {
	ChannelID string `json:"channelId"`
	MessageID string `json:"messageId"`
}

// ChatReadReceiptPayload announces that one person's read marker has moved.
//
// It carries the window it moved through rather than the message it stopped
// at, because the tick is a count of readers and a count must not be advanced
// twice for the same person. A reader crosses any given message exactly once,
// so a receiver can add one to every message of theirs that falls inside
// (From, To] and never double-count — which counting "everything at or before
// the new marker" would do on the reader's very next message.
//
// From is nil the first time somebody reads in a channel, meaning the window
// opens at the beginning.
type ChatReadReceiptPayload struct {
	ChannelID string     `json:"channelId"`
	UserID    string     `json:"userId"`
	From      *time.Time `json:"from"`
	To        time.Time  `json:"to"`
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
	// CallID is what the callee needs to join: the call page resolves a token
	// by call id, not by room name.
	CallID     string `json:"callId"`
	RoomName   string `json:"roomName"`
	CallerID   string `json:"callerId"`
	CallerName string `json:"callerName"`
	CallType   string `json:"callType"`
	Token      string `json:"token"` // LiveKit token
	// ChannelID names where the call came from, so a callee can tell a team
	// call from a one-to-one one.
	ChannelID string `json:"channelId,omitempty"`
}

type CallAcceptPayload struct {
	RoomName string `json:"roomName"`
}

type CallDeclinePayload struct {
	RoomName string `json:"roomName"`
	Reason   string `json:"reason,omitempty"`
}

type CallEndedPayload struct {
	// CallID identifies which call stopped, so a client that is ringing for it
	// knows to stop. Room name alone is not what the ringing dialog holds.
	CallID   string `json:"callId"`
	RoomName string `json:"roomName"`
	Duration int    `json:"duration"` // seconds
	// Reason distinguishes a call that was hung up from one somebody declined.
	Reason string `json:"reason,omitempty"`
	// DeclinedBy names who turned it down, for the caller's benefit.
	DeclinedBy string `json:"declinedBy,omitempty"`
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
		Payload:   MarshalPayload(payload),
		Timestamp: time.Now().UTC(),
		Version:   1,
	}
}

// MarshalPayload encodes an outbound payload to the raw JSON the envelope
// carries. A payload that cannot be encoded becomes null: every declared
// payload type is a plain struct of strings, times and slices, so this cannot
// happen for anything in this package, and dropping the frame entirely would
// be a worse answer than delivering one with an empty body.
func MarshalPayload(payload interface{}) json.RawMessage {
	switch p := payload.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return p
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return encoded
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

// Push is a notification delivered to a device, including one whose browser is
// closed. Declared here because the features that raise one and the service
// that sends it must both be able to name it without depending on each other.
type Push struct {
	Title              string         `json:"title"`
	Body               string         `json:"body"`
	Tag                string         `json:"tag,omitempty"`
	Renotify           bool           `json:"renotify,omitempty"`
	Data               map[string]any `json:"data,omitempty"`
	RequireInteraction bool           `json:"requireInteraction,omitempty"`
}

// CallKnockPayload tells a member that somebody asks to join. Connected says
// whether the member is in the call right now; when nobody is, the frame goes to
// the organizer and invitees wherever they are, alongside their notification.
type CallKnockPayload struct {
	CallID    string `json:"callId"`
	RequestID string `json:"requestId"`
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	Connected bool   `json:"connected"`
}

// CallKnockResultPayload tells the knocker what happened. Status is admitted,
// denied or ended. Admitted means POST /calls/:id/token now succeeds.
type CallKnockResultPayload struct {
	CallID    string `json:"callId"`
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
}

// CallKnockResolvedPayload clears the prompt on every other member.
type CallKnockResolvedPayload struct {
	CallID     string `json:"callId"`
	RequestID  string `json:"requestId"`
	Status     string `json:"status"`
	AnsweredBy string `json:"answeredBy"`
}
