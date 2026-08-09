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
	// NotifyMany writes one notification per recipient in a single statement,
	// for announcements that reach the whole organization.
	NotifyMany(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, kind, title, body string, data map[string]any) error
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
	// ReadCount is how many other people in the channel have read this, so a
	// sender can tell whether their message landed.
	ReadCount int        `json:"readCount"`
	CreatedAt time.Time  `json:"createdAt"`
	EditedAt  *time.Time `json:"editedAt,omitempty"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
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
	// CounterpartID is the other person in a direct message, so their
	// availability can be shown beside the conversation.
	CounterpartID *uuid.UUID `json:"counterpartId,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type ChannelMember struct {
	ChannelID   uuid.UUID  `json:"channelId"`
	UserID      uuid.UUID  `json:"userId"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	AvatarURL   *string    `json:"avatarUrl,omitempty"`
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

// SetLogger wires real logging. Left at the zero value every diagnostic in
// this package was written to a nil writer and silently discarded — including
// the fail-closed paths that swallow an error and deny access.
func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "chat").Logger()
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

	orgID, err := uuid.Parse(conn.OrgID())
	if err != nil {
		s.logger.Error().Err(err).Msg("Invalid org ID")
		return
	}

	if err := s.AuthorizePost(ctx, orgID, channelID, userID, false); err != nil {
		s.logger.Warn().Err(err).Str("user", conn.UserID()).Str("channel", payload.ChannelID).
			Msg("Refused to post in channel")
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
		// Addressed to the message's own channel: this used to broadcast to
		// "", which matches no channel, so an edit reached nobody.
		channelID, err := s.messageChannel(ctx, messageID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not resolve channel for edited message")
			return
		}
		s.broadcaster.BroadcastToChannel(channelID.String(), protocol.NewMessage(protocol.TypeChatMessage, protocol.ChatMessagePayload{
			Message: protocol.MessageData{
				ID:        messageID.String(),
				ChannelID: channelID.String(),
				Content:   payload.Content,
				EditedAt:  &now,
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
		channelID, err := s.messageChannel(ctx, messageID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not resolve channel for deleted message")
			return
		}
		s.broadcaster.BroadcastToChannel(channelID.String(), protocol.NewMessage(protocol.TypeChatDelete, payload), conn.UserID())
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
// sentinelStripper removes the control characters search uses to delimit a
// match, so a user cannot type one and forge a highlight in someone else's
// results.
var sentinelStripper = strings.NewReplacer(HighlightStart, "", HighlightEnd, "")

func (s *Service) createMessage(ctx context.Context, msg *Message) error {
	msg.Content = sentinelStripper.Replace(msg.Content)
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

	// The history query joins the author in, but a freshly sent message was
	// returned with an empty name, so the bubble the sender saw was unattributed
	// until the next refetch.
	if msg.UserName == "" {
		var avatar *string
		if err := s.db.QueryRow(ctx,
			`SELECT name, avatar_url FROM users WHERE id = $1`, msg.UserID).Scan(&msg.UserName, &avatar); err != nil {
			s.logger.Warn().Err(err).Msg("could not load message author")
		} else if avatar != nil {
			msg.UserAvatar = *avatar
		}
	}

	// Attachments are filed against the channel, which is what both the upload
	// URL and the listing use as their parent. Re-pointing them at the message
	// id used to orphan them: the row no longer matched a channel listing, and
	// the access check resolves a chat ref through channel_members, where a
	// message id can never match.

	s.notifyMentions(ctx, msg)
	// An announcement is meant to reach people who are not watching the
	// channel, so everybody in it hears about it — not only those named.
	s.NotifyAnnouncement(ctx, msg)
	return nil
}

func (s *Service) updateMessage(ctx context.Context, id uuid.UUID, content string, editedAt *time.Time) error {
	content = sentinelStripper.Replace(content)
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

// messageChannel resolves which channel a message belongs to, so an edit or a
// delete can be addressed to the same people who saw the original.
func (s *Service) messageChannel(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var channelID uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT channel_id FROM messages WHERE id = $1`, id).Scan(&channelID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, apperrors.ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("get message channel: %w", err)
	}
	return channelID, nil
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

// IsChannelAdmin reports whether the user owns or administers the channel.
func (s *Service) IsChannelAdmin(ctx context.Context, channelID, userID uuid.UUID) bool {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_members
			WHERE channel_id = $1 AND user_id = $2 AND role IN ('owner', 'admin'))
	`, channelID, userID).Scan(&ok)
	if err != nil {
		s.logger.Error().Err(err).Msg("channel admin check failed")
		return false
	}
	return ok
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
		  -- Without this a message id from another channel could advance this
		  -- channel's read marker, permanently zeroing the unread count.
		  AND m.channel_id = cm.channel_id
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
		ID:        msg.ID.String(),
		ChannelID: msg.ChannelID.String(),
		UserID:    msg.UserID.String(),
		// Carried through, or a message that arrives live renders with no
		// author until the page is reloaded.
		UserName:    msg.UserName,
		UserAvatar:  msg.UserAvatar,
		Content:     msg.Content,
		Attachments: s.attachmentsToProtocol(msg.Attachments),
		Reactions:   msg.Reactions,
		ReplyTo:     replyToString(msg.ReplyTo),
		CreatedAt:   msg.CreatedAt,
		EditedAt:    msg.EditedAt,
		DeletedAt:   msg.DeletedAt,
	}
}

func replyToString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
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
		            AND msg.created_at > COALESCE(cm.last_read_at, 'epoch'::timestamptz)),
		       -- A direct message has no name of its own; each side sees the
		       -- other person, and needs their id to show their availability.
		       (SELECT u.name FROM channel_members other
		          JOIN users u ON u.id = other.user_id
		          WHERE other.channel_id = c.id AND other.user_id <> $2
		          LIMIT 1),
		       (SELECT other.user_id FROM channel_members other
		          WHERE other.channel_id = c.id AND other.user_id <> $2
		          LIMIT 1)
		FROM channels c
		LEFT JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $2
		WHERE c.org_id = $1 AND c.deleted_at IS NULL AND COALESCE(c.is_archived, false) = false
		  AND (cm.user_id IS NOT NULL OR (
		        c.type IN ('public', 'announcement')
		        AND NOT EXISTS (SELECT 1 FROM channel_departures d
		                        WHERE d.channel_id = c.id AND d.user_id = $2)))
		ORDER BY c.created_at
	`, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	channels := []Channel{}
	for rows.Next() {
		var c Channel
		var counterpart *string
		var counterpartID *uuid.UUID
		if err := rows.Scan(&c.ID, &c.OrgID, &c.TeamID, &c.Name, &c.Type, &c.Description,
			&c.CreatedBy, &c.IsArchived, &c.IsPrivate, &c.MaxMembers,
			&c.CreatedAt, &c.UpdatedAt, &c.MemberCount, &c.UnreadCount,
			&counterpart, &counterpartID); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		if c.Type == "dm" {
			if counterpart != nil {
				c.Name = counterpart
			}
			c.CounterpartID = counterpartID
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
	`, orgID, teamID, name, channelType, description, userID, !isOpenToOrg(channelType)).Scan(
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

	// An org-wide channel belongs to everybody from the moment it exists.
	// Enrolling up front rather than on first visit is what lets an
	// announcement reach somebody who has never opened the channel.
	if isOpenToOrg(channelType) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_members (channel_id, user_id, role, joined_at)
			SELECT $1, u.id, 'member', NOW()
			FROM users u
			WHERE u.org_id = $2 AND u.deleted_at IS NULL AND u.id <> $3
			ON CONFLICT (channel_id, user_id) DO NOTHING
		`, c.ID, orgID, userID); err != nil {
			return nil, fmt.Errorf("enrol organization in channel: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM channel_members WHERE channel_id = $1`, c.ID).Scan(&c.MemberCount); err != nil {
		c.MemberCount = 1
	}
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

// isOpenToOrg reports whether every member of the organization belongs in a
// channel of this type. Public channels are open to all; announcement channels
// are too, but only their administrators may post.
func isOpenToOrg(channelType string) bool {
	return channelType == "public" || channelType == "announcement"
}

// EnsureMember gives the caller a membership row in an org-wide channel.
//
// Public channels were listed to the whole organization but only ever had the
// creator as a member, so everybody else could see the channel and was refused
// on every write — posting, attaching a file, even marking it read. Rather than
// special-casing "public" at each of those checks, anybody who opens an
// org-wide channel joins it. One row then makes membership, unread markers,
// member counts and attachment access all agree.
//
// Returns whether a row was actually created, so callers can announce a join
// exactly once.
func (s *Service) EnsureMember(ctx context.Context, orgID, channelID, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO channel_members (channel_id, user_id, role, joined_at)
		SELECT c.id, u.id, 'member', NOW()
		FROM channels c JOIN users u ON u.org_id = c.org_id
		WHERE c.id = $1 AND u.id = $2 AND c.org_id = $3
		  AND c.type IN ('public', 'announcement')
		  AND c.deleted_at IS NULL AND u.deleted_at IS NULL
		  AND COALESCE(c.is_archived, false) = false
		  -- Somebody who left stays out until they ask to rejoin.
		  AND NOT EXISTS (
		      SELECT 1 FROM channel_departures d
		      WHERE d.channel_id = c.id AND d.user_id = u.id)
		ON CONFLICT (channel_id, user_id) DO NOTHING
	`, channelID, userID, orgID)
	if err != nil {
		return false, fmt.Errorf("join open channel: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// OpenDirectMessage finds the one-to-one conversation between two people, or
// starts it. There is exactly one per pair, so opening a chat with somebody
// twice returns the same history rather than a second empty conversation.
//
// A DM has no name: both sides see the other person, which ListChannels fills
// in per viewer.
func (s *Service) OpenDirectMessage(ctx context.Context, orgID, userID, otherID uuid.UUID) (*Channel, error) {
	if userID == otherID {
		return nil, apperrors.New(apperrors.CodeBadRequest, "you cannot start a conversation with yourself")
	}

	var otherExists bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
		otherID, orgID).Scan(&otherExists); err != nil {
		return nil, fmt.Errorf("check other party: %w", err)
	}
	if !otherExists {
		return nil, apperrors.New(apperrors.CodeNotFound, "that person is not in this organization")
	}

	// A direct channel with exactly these two people in it.
	var existing uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT c.id
		FROM channels c
		JOIN channel_members mine ON mine.channel_id = c.id AND mine.user_id = $2
		JOIN channel_members theirs ON theirs.channel_id = c.id AND theirs.user_id = $3
		WHERE c.org_id = $1 AND c.type = 'dm' AND c.deleted_at IS NULL
		  AND (SELECT COUNT(*) FROM channel_members m WHERE m.channel_id = c.id) = 2
		LIMIT 1
	`, orgID, userID, otherID).Scan(&existing)
	if err == nil {
		return s.GetChannel(ctx, orgID, existing)
	}
	if err != pgx.ErrNoRows {
		return nil, fmt.Errorf("find direct message: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var c Channel
	if err := tx.QueryRow(ctx, `
		INSERT INTO channels (id, org_id, name, type, created_by, is_private, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, NULL, 'dm', $2, TRUE, NOW(), NOW())
		RETURNING id, org_id, team_id, name, type, description, created_by,
		          COALESCE(is_archived, false), COALESCE(is_private, true),
		          COALESCE(max_members, 1000), created_at, updated_at
	`, orgID, userID).Scan(&c.ID, &c.OrgID, &c.TeamID, &c.Name, &c.Type, &c.Description,
		&c.CreatedBy, &c.IsArchived, &c.IsPrivate, &c.MaxMembers, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create direct message: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_members (channel_id, user_id, role, joined_at)
		VALUES ($1, $2, 'owner', NOW()), ($1, $3, 'owner', NOW())
	`, c.ID, userID, otherID); err != nil {
		return nil, fmt.Errorf("add direct message members: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	c.MemberCount = 2
	return &c, nil
}

// EnrolInOpenChannels puts a user into every public and announcement channel
// in their organization. Called when somebody is added to the organization, so
// they arrive already in the channels that belong to everybody.
func (s *Service) EnrolInOpenChannels(ctx context.Context, orgID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO channel_members (channel_id, user_id, role, joined_at)
		SELECT c.id, $2, 'member', NOW()
		FROM channels c
		WHERE c.org_id = $1 AND c.type IN ('public', 'announcement')
		  AND c.deleted_at IS NULL AND COALESCE(c.is_archived, false) = false
		  AND NOT EXISTS (
		      SELECT 1 FROM channel_departures d
		      WHERE d.channel_id = c.id AND d.user_id = $2)
		ON CONFLICT (channel_id, user_id) DO NOTHING
	`, orgID, userID)
	if err != nil {
		return fmt.Errorf("enrol in open channels: %w", err)
	}
	return nil
}

// AuthorizePost decides whether someone may post in a channel, joining them to
// an org-wide channel on the way through. Both the REST handler and the
// WebSocket handler go through here so the two cannot drift apart.
//
// isOrgAdmin comes from the caller's organization roles, which the service
// cannot see for itself.
func (s *Service) AuthorizePost(ctx context.Context, orgID, channelID, userID uuid.UUID, isOrgAdmin bool) error {
	var channelType string
	err := s.db.QueryRow(ctx, `
		SELECT type FROM channels
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, channelID, orgID).Scan(&channelType)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.ErrNotFound
		}
		return fmt.Errorf("load channel: %w", err)
	}

	if isOpenToOrg(channelType) {
		if _, err := s.EnsureMember(ctx, orgID, channelID, userID); err != nil {
			return err
		}
		if channelType == "announcement" && !isOrgAdmin && !s.IsChannelAdmin(ctx, channelID, userID) {
			return apperrors.New(apperrors.CodeForbidden,
				"only administrators can post in an announcement channel")
		}
		return nil
	}

	if !s.isMember(ctx, channelID, userID) {
		return apperrors.New(apperrors.CodeForbidden, "you are not a member of this channel")
	}
	return nil
}

// AddMember is idempotent and refuses users from another organization.
//
// A direct message is a pair and stays one. Both sides are owners of it, so
// either could otherwise add a third person — after which the conversation no
// longer matches the two people it belongs to, and the next "new chat" between
// them starts a second one, splitting the history in half.
func (s *Service) AddMember(ctx context.Context, orgID, channelID, userID uuid.UUID, role string) error {
	if role == "" {
		role = "member"
	}

	var channelType string
	if err := s.db.QueryRow(ctx,
		`SELECT type FROM channels WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		channelID, orgID).Scan(&channelType); err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.New(apperrors.CodeNotFound, "channel or user not found in this organization")
		}
		return fmt.Errorf("load channel: %w", err)
	}
	if channelType == "dm" {
		return apperrors.New(apperrors.CodeBadRequest,
			"a direct message is between two people; start a group channel to include somebody else")
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

// ChannelMemberIDs answers who is in a channel, for live delivery. Kept
// separate from ListMembers because delivery runs on every message and only
// needs the ids.
func (s *Service) ChannelMemberIDs(ctx context.Context, channelID string) ([]string, error) {
	id, err := uuid.Parse(channelID)
	if err != nil {
		return nil, fmt.Errorf("invalid channel id: %w", err)
	}
	rows, err := s.db.Query(ctx, `
		SELECT cm.user_id FROM channel_members cm WHERE cm.channel_id = $1
	`, id)
	if err != nil {
		return nil, fmt.Errorf("list channel member ids: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("scan channel member id: %w", err)
		}
		ids = append(ids, userID.String())
	}
	return ids, rows.Err()
}

// ListMembers backs the channel settings panel, which had no way to find out
// who was in a channel — there was no route for it at all.
func (s *Service) ListMembers(ctx context.Context, orgID, channelID uuid.UUID) ([]ChannelMember, error) {
	rows, err := s.db.Query(ctx, `
		SELECT cm.channel_id, cm.user_id, u.name, u.email, u.avatar_url,
		       cm.role, cm.joined_at, cm.last_read_at, COALESCE(cm.notify_level, 'all')
		FROM channel_members cm
		JOIN users u ON u.id = cm.user_id
		JOIN channels c ON c.id = cm.channel_id
		WHERE cm.channel_id = $1 AND c.org_id = $2 AND u.deleted_at IS NULL
		ORDER BY CASE cm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, u.name
	`, channelID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list channel members: %w", err)
	}
	defer rows.Close()

	members := []ChannelMember{}
	for rows.Next() {
		var m ChannelMember
		if err := rows.Scan(&m.ChannelID, &m.UserID, &m.Name, &m.Email, &m.AvatarURL,
			&m.Role, &m.JoinedAt, &m.LastReadAt, &m.NotifyLevel); err != nil {
			return nil, fmt.Errorf("scan channel member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// NotifyAddedToChannel tells somebody they were put in a channel. Best effort:
// failing to notify must not undo the membership.
func (s *Service) NotifyAddedToChannel(ctx context.Context, orgID, channelID, userID, actorID uuid.UUID) {
	if s.notifier == nil || userID == actorID {
		return
	}
	var channelName, actorName string
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(c.name, 'a channel'), COALESCE(a.name, 'Somebody')
		FROM channels c LEFT JOIN users a ON a.id = $2
		WHERE c.id = $1
	`, channelID, actorID).Scan(&channelName, &actorName); err != nil {
		s.logger.Warn().Err(err).Msg("channel add notification lookup failed")
		return
	}
	if err := s.notifier.Notify(ctx, orgID, userID, "chat.added",
		"You were added to "+channelName,
		actorName+" added you to "+channelName,
		map[string]any{"channelId": channelID.String()}); err != nil {
		s.logger.Warn().Err(err).Msg("channel add notification failed")
	}
}

// NotifyAnnouncement tells everybody in an announcement channel that something
// was posted. Announcements are the one kind of message that is meant to reach
// people who are not looking at the channel.
func (s *Service) NotifyAnnouncement(ctx context.Context, msg *Message) {
	if s.notifier == nil {
		return
	}
	rows, err := s.db.Query(ctx, `
		SELECT cm.user_id, c.org_id, COALESCE(c.name, 'Announcements')
		FROM channel_members cm
		JOIN channels c ON c.id = cm.channel_id
		WHERE cm.channel_id = $1 AND cm.user_id <> $2 AND c.type = 'announcement'
	`, msg.ChannelID, msg.UserID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("announcement recipient lookup failed")
		return
	}
	defer rows.Close()

	recipients := []uuid.UUID{}
	var orgID uuid.UUID
	var channelName string
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID, &orgID, &channelName); err != nil {
			continue
		}
		recipients = append(recipients, userID)
	}
	if err := rows.Err(); err != nil {
		s.logger.Warn().Err(err).Msg("announcement recipient scan failed")
		return
	}
	if len(recipients) == 0 {
		return
	}

	// One statement rather than a round trip per person: an announcement
	// reaches everybody in the organization.
	if err := s.notifier.NotifyMany(ctx, orgID, recipients, "chat.announcement",
		channelName, msg.Content,
		map[string]any{"channelId": msg.ChannelID.String(), "messageId": msg.ID.String()}); err != nil {
		s.logger.Warn().Err(err).Msg("announcement notification failed")
	}
}

// RemoveMember takes somebody out of a channel and, for the org-wide kinds,
// records that they went so the automatic join does not put them straight back.
func (s *Service) RemoveMember(ctx context.Context, orgID, channelID, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		DELETE FROM channel_members cm USING channels c
		WHERE cm.channel_id = c.id AND cm.channel_id = $1 AND cm.user_id = $2 AND c.org_id = $3
	`, channelID, userID, orgID); err != nil {
		return fmt.Errorf("remove channel member: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_departures (channel_id, user_id, left_at)
		SELECT c.id, $2, NOW() FROM channels c
		WHERE c.id = $1 AND c.org_id = $3 AND c.type IN ('public', 'announcement')
		ON CONFLICT (channel_id, user_id) DO UPDATE SET left_at = NOW()
	`, channelID, userID, orgID); err != nil {
		return fmt.Errorf("record channel departure: %w", err)
	}

	return tx.Commit(ctx)
}

// BrowsableChannels lists the org-wide channels somebody is not currently in,
// so a channel they left can be found again.
func (s *Service) BrowsableChannels(ctx context.Context, orgID, userID uuid.UUID) ([]Channel, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.org_id, c.team_id, c.name, c.type, c.description, c.created_by,
		       COALESCE(c.is_archived, false), COALESCE(c.is_private, true),
		       COALESCE(c.max_members, 1000), c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM channel_members m WHERE m.channel_id = c.id)
		FROM channels c
		WHERE c.org_id = $1 AND c.deleted_at IS NULL
		  AND COALESCE(c.is_archived, false) = false
		  AND c.type IN ('public', 'announcement')
		  AND NOT EXISTS (SELECT 1 FROM channel_members cm
		                  WHERE cm.channel_id = c.id AND cm.user_id = $2)
		ORDER BY c.name
	`, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("list browsable channels: %w", err)
	}
	defer rows.Close()

	channels := []Channel{}
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.OrgID, &c.TeamID, &c.Name, &c.Type, &c.Description,
			&c.CreatedBy, &c.IsArchived, &c.IsPrivate, &c.MaxMembers,
			&c.CreatedAt, &c.UpdatedAt, &c.MemberCount); err != nil {
			return nil, fmt.Errorf("scan browsable channel: %w", err)
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

// RejoinChannel undoes a departure and puts somebody back into an org-wide
// channel, which is how they get back in after leaving.
func (s *Service) RejoinChannel(ctx context.Context, orgID, channelID, userID uuid.UUID) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM channel_departures WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID); err != nil {
		return fmt.Errorf("clear channel departure: %w", err)
	}
	joined, err := s.EnsureMember(ctx, orgID, channelID, userID)
	if err != nil {
		return err
	}
	if !joined && !s.isMember(ctx, channelID, userID) {
		return apperrors.New(apperrors.CodeNotFound, "channel not found in this organization")
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
		WHERE m.channel_id = $1
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
		// A deleted message keeps its place in the conversation but not its
		// contents: replies still have something to point at, and the gap is
		// visible rather than silent.
		if m.DeletedAt != nil {
			m.Content = ""
			m.Attachments = nil
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Queried newest-first for the LIMIT; the client renders oldest-first.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	withReactions, err := s.attachReactions(ctx, messages)
	if err != nil {
		return nil, err
	}
	withFiles, err := s.attachFiles(ctx, withReactions)
	if err != nil {
		return nil, err
	}
	return s.attachReadCounts(ctx, channelID, withFiles)
}

// attachFiles loads each message's attachments for a page in one query.
// History used to come back with none at all, so a file sent with a message
// vanished from the conversation as soon as the page reloaded.
func (s *Service) attachFiles(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	ids := make([]uuid.UUID, len(messages))
	index := make(map[uuid.UUID]int, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
		index[m.ID] = i
		messages[i].Attachments = []Attachment{}
	}

	rows, err := s.db.Query(ctx, `
		SELECT message_id, id, filename, content_type, size_bytes, object_key, thumbnail_key
		FROM attachments
		WHERE message_id = ANY($1) AND deleted_at IS NULL
		ORDER BY created_at
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("load message attachments: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var messageID uuid.UUID
		var a Attachment
		if err := rows.Scan(&messageID, &a.ID, &a.Filename, &a.ContentType,
			&a.SizeBytes, &a.ObjectKey, &a.ThumbnailKey); err != nil {
			return nil, fmt.Errorf("scan message attachment: %w", err)
		}
		if i, ok := index[messageID]; ok {
			messages[i].Attachments = append(messages[i].Attachments, a)
		}
	}
	return messages, rows.Err()
}

// ReadReceipt is one person who has read up to a message.
type ReadReceipt struct {
	UserID uuid.UUID `json:"userId"`
	Name   string    `json:"name"`
	ReadAt time.Time `json:"readAt"`
}

// ReadBy lists who has read a message, derived from how far each member has
// read rather than from a row per message per person — the read marker already
// says everything needed and costs nothing to keep.
//
// The author is left out: you have obviously read your own message.
func (s *Service) ReadBy(ctx context.Context, orgID, messageID uuid.UUID) ([]ReadReceipt, error) {
	rows, err := s.db.Query(ctx, `
		SELECT cm.user_id, u.name, cm.last_read_at
		FROM messages m
		JOIN channels c ON c.id = m.channel_id
		JOIN channel_members cm ON cm.channel_id = m.channel_id
		JOIN users u ON u.id = cm.user_id
		WHERE m.id = $1
		  AND c.org_id = $2
		  AND cm.user_id <> m.user_id
		  AND cm.last_read_at IS NOT NULL
		  AND cm.last_read_at >= m.created_at
		ORDER BY cm.last_read_at
	`, messageID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list who has read a message: %w", err)
	}
	defer rows.Close()

	receipts := []ReadReceipt{}
	for rows.Next() {
		var r ReadReceipt
		if err := rows.Scan(&r.UserID, &r.Name, &r.ReadAt); err != nil {
			return nil, fmt.Errorf("scan read receipt: %w", err)
		}
		receipts = append(receipts, r)
	}
	return receipts, rows.Err()
}

// attachReadCounts records, for each message, how many other people in the
// channel have read it — enough for a sender to see at a glance whether their
// message landed.
func (s *Service) attachReadCounts(ctx context.Context, channelID uuid.UUID, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT m.id, COUNT(cm.user_id)
		FROM messages m
		JOIN channel_members cm
		  ON cm.channel_id = m.channel_id
		 AND cm.user_id <> m.user_id
		 AND cm.last_read_at IS NOT NULL
		 AND cm.last_read_at >= m.created_at
		WHERE m.channel_id = $1 AND m.id = ANY($2)
		GROUP BY m.id
	`, channelID, messageIDs(messages))
	if err != nil {
		return nil, fmt.Errorf("count reads: %w", err)
	}
	defer rows.Close()

	counts := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan read count: %w", err)
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range messages {
		messages[i].ReadCount = counts[messages[i].ID]
	}
	return messages, nil
}

func messageIDs(messages []Message) []uuid.UUID {
	ids := make([]uuid.UUID, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
	}
	return ids
}

// bindAttachments claims uploads for a message. Only files already filed
// against this channel by this user are eligible, so a message cannot pull in
// somebody else's upload or one from another channel.
func (s *Service) bindAttachments(ctx context.Context, msg *Message, attachmentIDs []uuid.UUID) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	rows, err := s.db.Query(ctx, `
		UPDATE attachments
		SET message_id = $1
		WHERE id = ANY($2)
		  AND uploaded_by = $3
		  AND category = 'chat'
		  AND ref_id = $4
		  AND message_id IS NULL
		  AND deleted_at IS NULL
		RETURNING id, filename, content_type, size_bytes, object_key, thumbnail_key
	`, msg.ID, attachmentIDs, msg.UserID, msg.ChannelID.String())
	if err != nil {
		return fmt.Errorf("bind attachments: %w", err)
	}
	defer rows.Close()

	msg.Attachments = []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.Filename, &a.ContentType, &a.SizeBytes,
			&a.ObjectKey, &a.ThumbnailKey); err != nil {
			return fmt.Errorf("scan bound attachment: %w", err)
		}
		msg.Attachments = append(msg.Attachments, a)
	}
	return rows.Err()
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
func (s *Service) SendMessage(ctx context.Context, orgID, channelID, userID uuid.UUID, content string, replyTo *uuid.UUID, attachmentIDs []uuid.UUID, isOrgAdmin bool) (*Message, error) {
	if err := s.AuthorizePost(ctx, orgID, channelID, userID, isOrgAdmin); err != nil {
		return nil, err
	}
	// A message with nothing in it is not worth sending, but a file on its own
	// is: the attachment is the message.
	if strings.TrimSpace(content) == "" && len(attachmentIDs) == 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "write something or attach a file")
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
	if err := s.bindAttachments(ctx, msg, attachmentIDs); err != nil {
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
// EditWindow is how long a message stays editable. A conversation people have
// already read should not change underneath them, so corrections are for typos
// noticed straight away rather than for rewriting history.
const EditWindow = 5 * time.Minute

func (s *Service) EditMessage(ctx context.Context, messageID, userID uuid.UUID, content string) error {
	var owner uuid.UUID
	var createdAt time.Time
	var deletedAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT user_id, created_at, deleted_at FROM messages WHERE id = $1`, messageID).
		Scan(&owner, &createdAt, &deletedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.ErrNotFound
		}
		return fmt.Errorf("load message: %w", err)
	}
	if owner != userID {
		return apperrors.New(apperrors.CodeForbidden, "you can only edit your own messages")
	}
	if deletedAt != nil {
		return apperrors.New(apperrors.CodeBadRequest, "that message was deleted")
	}
	if time.Since(createdAt) > EditWindow {
		return apperrors.New(apperrors.CodeForbidden,
			"messages can only be edited for five minutes after sending")
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

// FileResult is a file somebody shared, with enough context to open it and to
// see where it came from.
type FileResult struct {
	ID           uuid.UUID  `json:"id"`
	Filename     string     `json:"filename"`
	ContentType  string     `json:"contentType"`
	SizeBytes    int64      `json:"sizeBytes"`
	ObjectKey    string     `json:"objectKey"`
	ChannelID    uuid.UUID  `json:"channelId"`
	ChannelName  string     `json:"channelName"`
	MessageID    *uuid.UUID `json:"messageId,omitempty"`
	UploadedBy   uuid.UUID  `json:"uploadedBy"`
	UploaderName string     `json:"uploaderName"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// SearchFiles finds shared files by name, in channels the caller can read.
// Names are short, so trigram similarity does the work on its own: an exact
// substring first, then anything close enough to be what was meant.
func (s *Service) SearchFiles(ctx context.Context, orgID, userID uuid.UUID, q SearchQuery) ([]FileResult, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return []FileResult{}, nil
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.filename, a.content_type, a.size_bytes, a.object_key,
		       c.id, COALESCE(c.name, ''), a.message_id,
		       a.uploaded_by, COALESCE(u.name, ''), a.created_at
		FROM attachments a
		JOIN channels c ON c.id = a.ref_id::uuid
		LEFT JOIN users u ON u.id = a.uploaded_by
		LEFT JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $3
		WHERE a.org_id = $1
		  AND a.category = 'chat'
		  AND a.deleted_at IS NULL
		  AND c.deleted_at IS NULL
		  AND (cm.user_id IS NOT NULL OR c.type = 'public')
		  AND (a.filename ILIKE '%' || $2 || '%' OR word_similarity($2, a.filename) >= 0.4)
		  AND ($4::uuid IS NULL OR c.id = $4)
		ORDER BY (a.filename ILIKE '%' || $2 || '%') DESC,
		         word_similarity($2, a.filename) DESC,
		         a.created_at DESC
		LIMIT $5 OFFSET $6
	`, orgID, text, userID, q.ChannelID, q.Limit, q.Offset)
	if err != nil {
		return nil, fmt.Errorf("search files: %w", err)
	}
	defer rows.Close()

	results := []FileResult{}
	for rows.Next() {
		var r FileResult
		if err := rows.Scan(&r.ID, &r.Filename, &r.ContentType, &r.SizeBytes, &r.ObjectKey,
			&r.ChannelID, &r.ChannelName, &r.MessageID,
			&r.UploadedBy, &r.UploaderName, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan file result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ── Message search ────────────────────────────────────────────────────────

// SearchQuery narrows a full-text search. Text is required; everything else
// is optional.
type SearchQuery struct {
	Text      string
	ChannelID *uuid.UUID
	UserID    *uuid.UUID
	Limit     int
	Offset    int
}

// SearchResult is a matching message with enough context to render a row and
// link back to it.
type SearchResult struct {
	ID          uuid.UUID `json:"id"`
	ChannelID   uuid.UUID `json:"channelId"`
	ChannelName string    `json:"channelName"`
	UserID      uuid.UUID `json:"userId"`
	UserName    string    `json:"userName"`
	Content     string    `json:"content"`
	Highlight   string    `json:"highlight"`
	CreatedAt   time.Time `json:"createdAt"`
}

// HighlightStart and HighlightEnd delimit a match inside SearchResult.
// Deliberately not HTML: ts_headline does not escape the message it is
// highlighting, so emitting <mark> would hand user-authored markup straight to
// the browser. The client splits on these and renders text.
const (
	HighlightStart = "\x02"
	HighlightEnd   = "\x03"
)

// SearchMessages runs a full-text search over the messages the caller may
// read: channels they belong to, plus the organization's public channels.
//
// websearch_to_tsquery gives the quoting and negation behaviour people expect
// from a search box, and never errors on malformed input the way plainto_ or
// to_tsquery can.
func (s *Service) SearchMessages(ctx context.Context, orgID, userID uuid.UUID, q SearchQuery) ([]SearchResult, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return []SearchResult{}, nil
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	// Whole-word matching first, then trigram similarity for everything it
	// misses — a typo, a partial word, a name spelled slightly differently.
	// Exact matches still sort above approximate ones.
	rows, err := s.db.Query(ctx, `
		SELECT m.id, m.channel_id, COALESCE(c.name, ''), m.user_id, u.name, m.content,
		       ts_headline('english', m.content, websearch_to_tsquery('english', $2),
		                   'StartSel=' || chr(2) || ', StopSel=' || chr(3) || ', MaxFragments=2, MaxWords=18, MinWords=5'),
		       m.created_at,
		       (m.content_search @@ websearch_to_tsquery('english', $2)) AS exact
		FROM messages m
		JOIN channels c ON c.id = m.channel_id
		JOIN users u ON u.id = m.user_id
		LEFT JOIN channel_members cm ON cm.channel_id = c.id AND cm.user_id = $3
		WHERE m.org_id = $1
		  AND m.deleted_at IS NULL
		  AND c.deleted_at IS NULL
		  AND (cm.user_id IS NOT NULL OR c.type = 'public')
		  AND (
		        m.content_search @@ websearch_to_tsquery('english', $2)
		        OR m.content ILIKE '%' || $2 || '%'
		        OR word_similarity($2, m.content) >= 0.4
		      )
		  AND ($4::uuid IS NULL OR m.channel_id = $4)
		  AND ($5::uuid IS NULL OR m.user_id = $5)
		ORDER BY (m.content_search @@ websearch_to_tsquery('english', $2)) DESC,
		         ts_rank_cd(m.content_search, websearch_to_tsquery('english', $2), 32) DESC,
		         word_similarity($2, m.content) DESC,
		         m.created_at DESC
		LIMIT $6 OFFSET $7
	`, orgID, text, userID, q.ChannelID, q.UserID, q.Limit, q.Offset)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	results := []SearchResult{}
	for rows.Next() {
		var r SearchResult
		var exact bool
		if err := rows.Scan(&r.ID, &r.ChannelID, &r.ChannelName, &r.UserID, &r.UserName,
			&r.Content, &r.Highlight, &r.CreatedAt, &exact); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		// ts_headline only marks up what the query actually matched, so an
		// approximate hit comes back with nothing highlighted. Show the message
		// rather than an empty fragment.
		if !exact {
			r.Highlight = ""
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
