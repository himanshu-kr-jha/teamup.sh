package chat

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/websocket"
	apperrors "github.com/teamup/teamup/pkg/errors"
	"github.com/teamup/teamup/pkg/protocol"
)

// Broadcaster defines the interface for broadcasting messages
type Broadcaster interface {
	BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string)
}

// Notifier raises a notification for a user. An interface so chat does not
// depend on the notifications package.
type Notifier interface {
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

type Service struct {
	db          *pgxpool.Pool
	redis       *redis.Client
	logger      zerolog.Logger
	broadcaster Broadcaster
	notifier    Notifier
}

func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// mentionPattern matches @-mentions of a channel member by name.
var mentionPattern = regexp.MustCompile(`@([A-Za-z0-9._-]+)`)

// notifyMentions raises a notification for each channel member named in the
// message. Best effort, and never notifies the author about themselves.
func (s *Service) notifyMentions(ctx context.Context, msg *Message) {
	if s.notifier == nil {
		return
	}
	names := mentionPattern.FindAllStringSubmatch(msg.Content, -1)
	if len(names) == 0 {
		return
	}
	handles := make([]string, 0, len(names))
	for _, m := range names {
		handles = append(handles, m[1])
	}

	rows, err := s.db.Query(ctx, `
		SELECT u.id, c.org_id, COALESCE(c.name, 'a channel')
		FROM channel_members cm
		JOIN users u ON u.id = cm.user_id
		JOIN channels c ON c.id = cm.channel_id
		WHERE cm.channel_id = $1 AND u.id <> $2
		  AND REPLACE(LOWER(u.name), ' ', '') = ANY($3)
	`, msg.ChannelID, msg.UserID, lowerAll(handles))
	if err != nil {
		s.logger.Warn().Err(err).Msg("mention lookup failed")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var userID, orgID uuid.UUID
		var channelName string
		if err := rows.Scan(&userID, &orgID, &channelName); err != nil {
			continue
		}
		if err := s.notifier.Notify(ctx, orgID, userID, "chat.mention",
			"You were mentioned in "+channelName, msg.Content,
			map[string]any{"channelId": msg.ChannelID.String(), "messageId": msg.ID.String()}); err != nil {
			s.logger.Warn().Err(err).Msg("mention notification failed")
		}
	}
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

// JSON tags matter here: these structs are serialised straight onto the wire
// and the frontend reads camelCase.
type Message struct {
	ID          uuid.UUID           `json:"id"`
	ChannelID   uuid.UUID           `json:"channelId"`
	UserID      uuid.UUID           `json:"userId"`
	UserName    string              `json:"userName"`
	UserAvatar  string              `json:"userAvatar,omitempty"`
	Content     string              `json:"content"`
	ReplyTo     *uuid.UUID          `json:"replyTo,omitempty"`
	Attachments []Attachment        `json:"attachments"`
	Reactions   map[string][]string `json:"reactions"`
	CreatedAt   time.Time           `json:"createdAt"`
	EditedAt    *time.Time          `json:"editedAt,omitempty"`
	DeletedAt   *time.Time          `json:"deletedAt,omitempty"`
}

type Attachment struct {
	ID           string  `json:"id"`
	Filename     string  `json:"filename"`
	ContentType  string  `json:"contentType"`
	SizeBytes    int64   `json:"sizeBytes"`
	ObjectKey    string  `json:"objectKey"`
	ThumbnailKey *string `json:"thumbnailKey,omitempty"`
}

type Channel struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"orgId"`
	TeamID      *uuid.UUID `json:"teamId,omitempty"`
	Name        *string    `json:"name"`
	Type        string     `json:"type"`
	Description *string    `json:"description,omitempty"`
	CreatedBy   uuid.UUID  `json:"createdBy"`
	IsArchived  bool       `json:"isArchived"`
	IsPrivate   bool       `json:"isPrivate"`
	MaxMembers  int        `json:"maxMembers"`
	MemberCount int        `json:"memberCount"`
	UnreadCount int        `json:"unreadCount"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type ChannelMember struct {
	ChannelID   uuid.UUID  `json:"channelId"`
	UserID      uuid.UUID  `json:"userId"`
	Role        string     `json:"role"`
	JoinedAt    time.Time  `json:"joinedAt"`
	LastReadAt  *time.Time `json:"lastReadAt,omitempty"`
	NotifyLevel string     `json:"notifyLevel"`
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

// ── Persistence ───────────────────────────────────────────────────────────
//
// The WebSocket handlers above were fully implemented and broadcast to
// connected clients, but every call below used to be a no-op. Messages
// therefore appeared live and vanished on reload.

// createMessage stores a message. org_id is derived from the channel rather
// than trusted from the caller.
func (s *Service) createMessage(ctx context.Context, msg *Message) error {
	err := s.db.QueryRow(ctx, `
		INSERT INTO messages (id, org_id, channel_id, user_id, content, reply_to, created_at)
		SELECT $1, c.org_id, $2, $3, $4, $5, $6
		FROM channels c WHERE c.id = $2 AND c.deleted_at IS NULL
		RETURNING created_at
	`, msg.ID, msg.ChannelID, msg.UserID, msg.Content, msg.ReplyTo, msg.CreatedAt).Scan(&msg.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.New(apperrors.CodeNotFound, "channel not found")
		}
		return fmt.Errorf("create message: %w", err)
	}

	for _, a := range msg.Attachments {
		if _, err := s.db.Exec(ctx, `
			UPDATE attachments SET ref_id = $1 WHERE object_key = $2
		`, msg.ID.String(), a.ObjectKey); err != nil {
			return fmt.Errorf("link attachment: %w", err)
		}
	}

	s.notifyMentions(ctx, msg)
	return nil
}

func (s *Service) updateMessage(ctx context.Context, id uuid.UUID, content string, editedAt *time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE messages SET content = $2, edited_at = $3
		WHERE id = $1 AND deleted_at IS NULL
	`, id, content, editedAt)
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// deleteMessage is a soft delete so replies keep their parent and history
// stays contiguous.
func (s *Service) deleteMessage(ctx context.Context, id uuid.UUID, deletedAt *time.Time) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE messages SET deleted_at = $2 WHERE id = $1 AND deleted_at IS NULL`, id, deletedAt)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func (s *Service) getMessageOwner(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var owner uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT user_id FROM messages WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&owner)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, apperrors.ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("get message owner: %w", err)
	}
	return owner, nil
}

// isChannelAdmin lets a channel owner or admin moderate someone else's
// message.
func (s *Service) isChannelAdmin(ctx context.Context, messageID, userID uuid.UUID) bool {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM messages m
			JOIN channel_members cm ON cm.channel_id = m.channel_id
			WHERE m.id = $1 AND cm.user_id = $2 AND cm.role IN ('owner', 'admin'))
	`, messageID, userID).Scan(&ok)
	if err != nil {
		s.logger.Error().Err(err).Msg("channel admin check failed")
		return false
	}
	return ok
}

// updateLastRead advances the member's read marker, never backwards, so an
// out-of-order acknowledgement cannot resurrect unread counts.
func (s *Service) updateLastRead(ctx context.Context, channelID, userID uuid.UUID, messageID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE channel_members cm
		SET last_read_at = GREATEST(COALESCE(cm.last_read_at, 'epoch'::timestamptz), m.created_at)
		FROM messages m
		WHERE m.id = $3 AND cm.channel_id = $1 AND cm.user_id = $2
	`, channelID, userID, messageID)
	if err != nil {
		return fmt.Errorf("update last read: %w", err)
	}
	return nil
}

// isMember previously returned true for everyone, so any authenticated user
// could post into any channel over the WebSocket.
func (s *Service) isMember(ctx context.Context, channelID, userID uuid.UUID) bool {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_members cm
			JOIN channels c ON c.id = cm.channel_id
			WHERE cm.channel_id = $1 AND cm.user_id = $2 AND c.deleted_at IS NULL)
	`, channelID, userID).Scan(&ok)
	if err != nil {
		s.logger.Error().Err(err).Msg("channel membership check failed")
		return false
	}
	return ok
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

// ── Channel and message API ──────────────────────────────────────────────

// ListChannels returns the channels the user can actually see: the ones they
// belong to, plus the organization's public channels.
func (s *Service) ListChannels(ctx context.Context, orgID, userID uuid.UUID) ([]Channel, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.org_id, c.team_id, c.name, c.type, c.description, c.created_by,
		       COALESCE(c.is_archived, false), COALESCE(c.is_private, true),
		       COALESCE(c.max_members, 1000), c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM channel_members m WHERE m.channel_id = c.id),
		       (SELECT COUNT(*) FROM messages msg
		          WHERE msg.channel_id = c.id AND msg.deleted_at IS NULL
		            AND msg.user_id <> $2
		            AND msg.created_at > COALESCE(cm.last_read_at, 'epoch'::timestamptz))
		FROM channels c
		LEFT JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $2
		WHERE c.org_id = $1 AND c.deleted_at IS NULL AND COALESCE(c.is_archived, false) = false
		  AND (cm.user_id IS NOT NULL OR c.type = 'public')
		ORDER BY c.created_at
	`, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	channels := []Channel{}
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.OrgID, &c.TeamID, &c.Name, &c.Type, &c.Description,
			&c.CreatedBy, &c.IsArchived, &c.IsPrivate, &c.MaxMembers,
			&c.CreatedAt, &c.UpdatedAt, &c.MemberCount, &c.UnreadCount); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

// CreateChannel makes the creator a channel owner in the same transaction, so
// a channel can never exist with nobody able to administer it.
func (s *Service) CreateChannel(ctx context.Context, orgID, userID uuid.UUID, name *string, channelType string, description *string, teamID *uuid.UUID) (*Channel, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var c Channel
	err = tx.QueryRow(ctx, `
		INSERT INTO channels (id, org_id, team_id, name, type, description, created_by, is_private, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		RETURNING id, org_id, team_id, name, type, description, created_by,
		          COALESCE(is_archived, false), COALESCE(is_private, true),
		          COALESCE(max_members, 1000), created_at, updated_at
	`, orgID, teamID, name, channelType, description, userID, channelType != "public").Scan(
		&c.ID, &c.OrgID, &c.TeamID, &c.Name, &c.Type, &c.Description, &c.CreatedBy,
		&c.IsArchived, &c.IsPrivate, &c.MaxMembers, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create channel: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_members (channel_id, user_id, role, joined_at)
		VALUES ($1, $2, 'owner', NOW())
	`, c.ID, userID); err != nil {
		return nil, fmt.Errorf("add creator to channel: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	c.MemberCount = 1
	return &c, nil
}

// GetChannel is scoped to the organization so an id from another tenant reads
// as not-found rather than leaking the row.
func (s *Service) GetChannel(ctx context.Context, orgID, id uuid.UUID) (*Channel, error) {
	var c Channel
	err := s.db.QueryRow(ctx, `
		SELECT c.id, c.org_id, c.team_id, c.name, c.type, c.description, c.created_by,
		       COALESCE(c.is_archived, false), COALESCE(c.is_private, true),
		       COALESCE(c.max_members, 1000), c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM channel_members m WHERE m.channel_id = c.id)
		FROM channels c
		WHERE c.id = $1 AND c.org_id = $2 AND c.deleted_at IS NULL
	`, id, orgID).Scan(&c.ID, &c.OrgID, &c.TeamID, &c.Name, &c.Type, &c.Description,
		&c.CreatedBy, &c.IsArchived, &c.IsPrivate, &c.MaxMembers,
		&c.CreatedAt, &c.UpdatedAt, &c.MemberCount)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("get channel: %w", err)
	}
	return &c, nil
}

func (s *Service) UpdateChannel(ctx context.Context, orgID, channelID uuid.UUID, name, description *string, isPrivate *bool) (*Channel, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE channels SET name = COALESCE($3, name), description = COALESCE($4, description),
		                    is_private = COALESCE($5, is_private), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, channelID, orgID, name, description, isPrivate)
	if err != nil {
		return nil, fmt.Errorf("update channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperrors.ErrNotFound
	}
	return s.GetChannel(ctx, orgID, channelID)
}

func (s *Service) ArchiveChannel(ctx context.Context, orgID, channelID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE channels SET is_archived = TRUE, deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, channelID, orgID)
	if err != nil {
		return fmt.Errorf("archive channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// AddMember is idempotent and refuses users from another organization.
func (s *Service) AddMember(ctx context.Context, orgID, channelID, userID uuid.UUID, role string) error {
	if role == "" {
		role = "member"
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO channel_members (channel_id, user_id, role, joined_at)
		SELECT c.id, u.id, $4, NOW()
		FROM channels c JOIN users u ON u.org_id = c.org_id
		WHERE c.id = $1 AND u.id = $2 AND c.org_id = $3
		  AND c.deleted_at IS NULL AND u.deleted_at IS NULL
		ON CONFLICT (channel_id, user_id) DO NOTHING
	`, channelID, userID, orgID, role)
	if err != nil {
		return fmt.Errorf("add channel member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM channel_members WHERE channel_id = $1 AND user_id = $2)`,
			channelID, userID).Scan(&exists); err != nil {
			return fmt.Errorf("verify channel membership: %w", err)
		}
		if !exists {
			return apperrors.New(apperrors.CodeNotFound, "channel or user not found in this organization")
		}
	}
	return nil
}

func (s *Service) RemoveMember(ctx context.Context, orgID, channelID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM channel_members cm USING channels c
		WHERE cm.channel_id = c.id AND cm.channel_id = $1 AND cm.user_id = $2 AND c.org_id = $3
	`, channelID, userID, orgID)
	if err != nil {
		return fmt.Errorf("remove channel member: %w", err)
	}
	return nil
}

// GetMessages returns a page of history oldest-first, paging backwards through
// `before` so a client can walk into the past.
func (s *Service) GetMessages(ctx context.Context, channelID uuid.UUID, limit int, before *time.Time) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT m.id, m.channel_id, m.user_id, m.content, m.reply_to,
		       m.created_at, m.edited_at, m.deleted_at,
		       u.name, COALESCE(u.avatar_url, '')
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.channel_id = $1 AND m.deleted_at IS NULL
		  AND ($2::timestamptz IS NULL OR m.created_at < $2)
		ORDER BY m.created_at DESC
		LIMIT $3
	`, channelID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	messages := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Content, &m.ReplyTo,
			&m.CreatedAt, &m.EditedAt, &m.DeletedAt, &m.UserName, &m.UserAvatar); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Reactions = map[string][]string{}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Queried newest-first for the LIMIT; the client renders oldest-first.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return s.attachReactions(ctx, messages)
}

// attachReactions loads reactions for a page in one query rather than per row.
func (s *Service) attachReactions(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	ids := make([]uuid.UUID, len(messages))
	index := make(map[uuid.UUID]int, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
		index[m.ID] = i
	}

	rows, err := s.db.Query(ctx, `
		SELECT message_id, emoji, user_id FROM message_reactions WHERE message_id = ANY($1)
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("load reactions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var messageID, userID uuid.UUID
		var emoji string
		if err := rows.Scan(&messageID, &emoji, &userID); err != nil {
			return nil, fmt.Errorf("scan reaction: %w", err)
		}
		if i, ok := index[messageID]; ok {
			messages[i].Reactions[emoji] = append(messages[i].Reactions[emoji], userID.String())
		}
	}
	return messages, rows.Err()
}

func (s *Service) AddReaction(ctx context.Context, messageID, userID uuid.UUID, emoji string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO message_reactions (message_id, user_id, emoji, created_at)
		SELECT m.id, cm.user_id, $3, NOW()
		FROM messages m JOIN channel_members cm ON cm.channel_id = m.channel_id
		WHERE m.id = $1 AND cm.user_id = $2 AND m.deleted_at IS NULL
		ON CONFLICT DO NOTHING
	`, messageID, userID, emoji)
	if err != nil {
		return fmt.Errorf("add reaction: %w", err)
	}
	return nil
}

func (s *Service) RemoveReaction(ctx context.Context, messageID, userID uuid.UUID, emoji string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM message_reactions WHERE message_id = $1 AND user_id = $2 AND emoji = $3`,
		messageID, userID, emoji)
	if err != nil {
		return fmt.Errorf("remove reaction: %w", err)
	}
	return nil
}

// SendMessage is the HTTP path onto the same storage the WebSocket uses, so a
// client without a live socket still gets a persisted, broadcast message.
func (s *Service) SendMessage(ctx context.Context, channelID, userID uuid.UUID, content string, replyTo *uuid.UUID) (*Message, error) {
	if !s.isMember(ctx, channelID, userID) {
		return nil, apperrors.New(apperrors.CodeForbidden, "you are not a member of this channel")
	}
	msg := &Message{
		ID:        uuid.New(),
		ChannelID: channelID,
		UserID:    userID,
		Content:   content,
		ReplyTo:   replyTo,
		Reactions: map[string][]string{},
		CreatedAt: time.Now(),
	}
	if err := s.createMessage(ctx, msg); err != nil {
		return nil, err
	}
	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(channelID.String(), protocol.NewMessage(
			protocol.TypeChatMessage, protocol.ChatMessagePayload{Message: s.messageToData(msg)},
		), userID.String())
	}
	return msg, nil
}

// EditMessage and DeleteMessage enforce authorship (or channel moderation)
// before touching the row.
func (s *Service) EditMessage(ctx context.Context, messageID, userID uuid.UUID, content string) error {
	owner, err := s.getMessageOwner(ctx, messageID)
	if err != nil {
		return err
	}
	if owner != userID {
		return apperrors.New(apperrors.CodeForbidden, "you can only edit your own messages")
	}
	now := time.Now()
	return s.updateMessage(ctx, messageID, content, &now)
}

func (s *Service) DeleteMessage(ctx context.Context, messageID, userID uuid.UUID) error {
	owner, err := s.getMessageOwner(ctx, messageID)
	if err != nil {
		return err
	}
	if owner != userID && !s.isChannelAdmin(ctx, messageID, userID) {
		return apperrors.New(apperrors.CodeForbidden, "you cannot delete this message")
	}
	now := time.Now()
	return s.deleteMessage(ctx, messageID, &now)
}

// MarkRead advances the caller's read marker to the given message.
func (s *Service) MarkRead(ctx context.Context, channelID, userID, messageID uuid.UUID) error {
	return s.updateLastRead(ctx, channelID, userID, messageID)
}
