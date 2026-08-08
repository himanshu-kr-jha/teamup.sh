package chat

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/websocket"
	"github.com/teamup/teamup/pkg/protocol"
)

// Broadcaster defines the interface for broadcasting messages
type Broadcaster interface {
	BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string)
}

type Service struct {
	db          *pgxpool.Pool
	redis       *redis.Client
	logger      zerolog.Logger
	broadcaster Broadcaster
}

type Message struct {
	ID          uuid.UUID
	ChannelID   uuid.UUID
	UserID      uuid.UUID
	Content     string
	ReplyTo     *uuid.UUID
	Attachments []Attachment
	Reactions   map[string][]string
	CreatedAt   time.Time
	EditedAt    *time.Time
	DeletedAt   *time.Time
}

type Attachment struct {
	ID           string
	Filename     string
	ContentType  string
	SizeBytes    int64
	ObjectKey    string
	ThumbnailKey *string
}

type Channel struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	TeamID      *uuid.UUID
	Name        *string
	Type        string
	Description *string
	CreatedBy   uuid.UUID
	IsArchived  bool
	IsPrivate   bool
	MaxMembers  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ChannelMember struct {
	ChannelID   uuid.UUID
	UserID      uuid.UUID
	Role        string
	JoinedAt    time.Time
	LastReadAt  *time.Time
	NotifyLevel string
}

func NewService(db *pgxpool.Pool, redis *redis.Client) *Service {
	return &Service{
		db:    db,
		redis: redis,
	}
}

func (s *Service) SetBroadcaster(b Broadcaster) {
	s.broadcaster = b
}

// HandleSendMessage processes a chat.send message
func (s *Service) HandleSendMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatSendPayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		s.logger.Error().Msg("Invalid chat.send payload: payload is not []byte")
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		s.logger.Error().Err(err).Msg("Invalid chat.send payload")
		return
	}

	channelID, err := uuid.Parse(payload.ChannelID)
	if err != nil {
		s.logger.Error().Err(err).Msg("Invalid channel ID")
		return
	}

	userID, err := uuid.Parse(conn.UserID())
	if err != nil {
		s.logger.Error().Err(err).Msg("Invalid user ID")
		return
	}

	if !s.isMember(ctx, channelID, userID) {
		s.logger.Warn().Str("user", conn.UserID()).Str("channel", payload.ChannelID).Msg("User not member of channel")
		return
	}

	message := &Message{
		ID:          uuid.New(),
		ChannelID:   channelID,
		UserID:      userID,
		Content:     payload.Content,
		Attachments: s.convertAttachments(payload.Attachments),
		Reactions:   make(map[string][]string),
		CreatedAt:   time.Now(),
	}

	if payload.ReplyTo != nil {
		replyToID, err := uuid.Parse(*payload.ReplyTo)
		if err == nil {
			message.ReplyTo = &replyToID
		}
	}

	if err := s.createMessage(ctx, message); err != nil {
		s.logger.Error().Err(err).Msg("Failed to save message")
		return
	}

	conn.Enqueue(protocol.NewMessage(protocol.TypeChatSent, protocol.ChatSentPayload{
		TempID:    payload.TempID,
		MessageID: message.ID.String(),
		Timestamp: message.CreatedAt,
	}))

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(payload.ChannelID, protocol.NewMessage(protocol.TypeChatMessage, protocol.ChatMessagePayload{
			Message: s.messageToData(message),
		}), conn.UserID())
	}
}

func (s *Service) HandleEditMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatEditPayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	messageID, err := uuid.Parse(payload.MessageID)
	if err != nil {
		return
	}

	userID, err := uuid.Parse(conn.UserID())
	if err != nil {
		return
	}
	ownerID, err := s.getMessageOwner(ctx, messageID)
	if err != nil || ownerID != userID {
		return
	}

	now := time.Now()
	if err := s.updateMessage(ctx, messageID, payload.Content, &now); err != nil {
		return
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel("", protocol.NewMessage(protocol.TypeChatMessage, protocol.ChatMessagePayload{
			Message: protocol.MessageData{
				ID:       messageID.String(),
				Content:  payload.Content,
				EditedAt: &now,
			},
		}), conn.UserID())
	}
}

func (s *Service) HandleDeleteMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatDeletePayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	messageID, err := uuid.Parse(payload.MessageID)
	if err != nil {
		return
	}

	ownerID, err := s.getMessageOwner(ctx, messageID)
	if err != nil {
		return
	}

	userID, _ := uuid.Parse(conn.UserID())
	isAdmin := s.isChannelAdmin(ctx, messageID, userID)
	if ownerID != userID && !isAdmin {
		return
	}

	now := time.Now()
	if err := s.deleteMessage(ctx, messageID, &now); err != nil {
		return
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel("", protocol.NewMessage(protocol.TypeChatDelete, payload), conn.UserID())
	}
}

func (s *Service) HandleTyping(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatTypingPayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(payload.ChannelID, protocol.NewMessage(protocol.TypeChatTyping, payload), conn.UserID())
	}
}

func (s *Service) HandleRead(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatReadPayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	channelID, _ := uuid.Parse(payload.ChannelID)
	messageID, _ := uuid.Parse(payload.MessageID)
	userID, _ := uuid.Parse(conn.UserID())
	s.updateLastRead(ctx, channelID, userID, messageID)
}

// Database operations (stubs)
func (s *Service) createMessage(ctx context.Context, msg *Message) error {
	return nil
}

func (s *Service) updateMessage(ctx context.Context, id uuid.UUID, content string, editedAt *time.Time) error {
	return nil
}

func (s *Service) deleteMessage(ctx context.Context, id uuid.UUID, deletedAt *time.Time) error {
	return nil
}

func (s *Service) getMessageOwner(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (s *Service) isChannelAdmin(ctx context.Context, messageID, userID uuid.UUID) bool {
	return false
}

func (s *Service) updateLastRead(ctx context.Context, channelID, userID uuid.UUID, messageID uuid.UUID) error {
	return nil
}

func (s *Service) isMember(ctx context.Context, channelID, userID uuid.UUID) bool {
	return true
}

func (s *Service) convertAttachments(atts []protocol.Attachment) []Attachment {
	result := make([]Attachment, len(atts))
	for i, a := range atts {
		result[i] = Attachment{
			ID:          a.ObjectKey,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			SizeBytes:   a.SizeBytes,
			ObjectKey:   a.ObjectKey,
		}
	}
	return result
}

func (s *Service) messageToData(msg *Message) protocol.MessageData {
	return protocol.MessageData{
		ID:          msg.ID.String(),
		ChannelID:   msg.ChannelID.String(),
		UserID:      msg.UserID.String(),
		Content:     msg.Content,
		Attachments: s.attachmentsToProtocol(msg.Attachments),
		Reactions:   msg.Reactions,
		CreatedAt:   msg.CreatedAt,
		EditedAt:    msg.EditedAt,
		DeletedAt:   msg.DeletedAt,
	}
}

func (s *Service) attachmentsToProtocol(atts []Attachment) []protocol.Attachment {
	result := make([]protocol.Attachment, len(atts))
	for i, a := range atts {
		result[i] = protocol.Attachment{
			ID:          a.ID,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			SizeBytes:   a.SizeBytes,
			ObjectKey:   a.ObjectKey,
		}
	}
	return result
}

// API handlers
func (s *Service) ListChannels(ctx context.Context, orgID uuid.UUID) ([]Channel, error) {
	return nil, nil
}

func (s *Service) CreateChannel(ctx context.Context, orgID, userID uuid.UUID, name *string, channelType string, description *string, teamID *uuid.UUID) (*Channel, error) {
	return nil, nil
}

func (s *Service) GetChannel(ctx context.Context, id uuid.UUID) (*Channel, error) {
	return nil, nil
}

func (s *Service) AddMember(ctx context.Context, channelID, userID uuid.UUID, role string) error {
	return nil
}

func (s *Service) GetMessages(ctx context.Context, channelID uuid.UUID, limit int, before *time.Time) ([]Message, error) {
	return nil, nil
}
