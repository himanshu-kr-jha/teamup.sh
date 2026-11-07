package chat

import (
	"context"
	"encoding/json"
	"errors"
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
	// PushToUsers reaches devices without recording anything in the bell, for
	// the things that happen too often to list.
	PushToUsers(ctx context.Context, userIDs []uuid.UUID, payload protocol.Push)
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
	// Kind separates what somebody wrote from what happened: "user" or
	// "event". An event is shown as a note in the conversation rather than as
	// a message from a person.
	Kind string `json:"kind"`
	// ReadCount is how many other people in the channel have read this, so a
	// sender can tell whether their message landed.
	ReadCount int `json:"readCount"`
	// ReplyCount and LastReplyAt describe the thread hanging off this message.
	// Only ever set on a thread root, because a reply cannot have replies.
	ReplyCount  int        `json:"replyCount"`
	LastReplyAt *time.Time `json:"lastReplyAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	EditedAt    *time.Time `json:"editedAt,omitempty"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
}

// Thread is one conversation hanging off a message: the message it started
// from, and everything said in reply to it.
type Thread struct {
	ChannelID uuid.UUID `json:"channelId"`
	Root      Message   `json:"root"`
	Replies   []Message `json:"replies"`
}

// replyCountSQL counts a page's replies in one round trip.
//
// One query for the whole page rather than one per message: counting replies
// per row is fifty round trips for a fifty-message page, on the query that
// already runs on every channel open, every socket reconnect and every tab
// focus. `reply_to = ANY($1)` is a strict comparison, so the planner can prove
// it excludes NULLs and use the partial index over reply_to.
//
// MAX(created_at) comes back with the count because "3 replies" is worth much
// less than "3 replies, the last an hour ago", and it is free here.
const replyCountSQL = `
	SELECT reply_to, COUNT(*), MAX(created_at)
	FROM messages
	WHERE reply_to = ANY($1)
	GROUP BY reply_to`

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
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
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
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
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
	// The same rules as the REST edit, and the same broadcast.
	if err := s.EditMessage(ctx, messageID, userID, payload.Content); err != nil {
		s.logger.Warn().Err(err).Str("user", conn.UserID()).Str("message", payload.MessageID).
			Msg("Refused to edit message")
	}
}

func (s *Service) HandleDeleteMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatDeletePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
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
	if err := s.DeleteMessage(ctx, messageID, userID); err != nil {
		s.logger.Warn().Err(err).Str("user", conn.UserID()).Str("message", payload.MessageID).
			Msg("Refused to delete message")
	}
}

// HandleTyping shows somebody as typing to the rest of their channel.
//
// The channel id comes from the client, and this was the one chat handler that
// took it on trust — no membership check, no organization parse. Anybody could
// therefore appear to be typing in any channel on the instance, including a
// private one they are not in and one belonging to another organization, which
// both leaks that the channel exists and injects state into it. Gated on the
// same question as posting, since that is what a typing indicator promises is
// about to happen.
func (s *Service) HandleTyping(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatTypingPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	channelID, err := uuid.Parse(payload.ChannelID)
	if err != nil {
		return
	}
	userID, err := uuid.Parse(conn.UserID())
	if err != nil {
		return
	}
	orgID, err := uuid.Parse(conn.OrgID())
	if err != nil {
		return
	}

	if err := s.AuthorizePost(ctx, orgID, channelID, userID, false); err != nil {
		s.logger.Warn().Err(err).Str("user", conn.UserID()).Str("channel", payload.ChannelID).
			Msg("Refused to show typing in channel")
		return
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(payload.ChannelID, protocol.NewMessage(protocol.TypeChatTyping, payload), conn.UserID())
	}
}

func (s *Service) HandleRead(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.ChatReadPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	channelID, _ := uuid.Parse(payload.ChannelID)
	messageID, _ := uuid.Parse(payload.MessageID)
	userID, _ := uuid.Parse(conn.UserID())
	from, to, advanced, err := s.updateLastRead(ctx, channelID, userID, messageID)
	if err != nil {
		s.logger.Warn().Err(err).Str("channel", payload.ChannelID).Msg("could not advance the read marker")
		return
	}
	if advanced {
		s.announceRead(channelID, userID, from, to)
	}
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

// threadRootFor decides which thread a reply joins, and refuses a parent that
// is not in the same channel.
//
// A thread here is one level deep, the way it is in Slack: a reply to a reply
// joins the conversation it was already in. reply_to points at a single
// parent, so nothing in the schema stops a chain, and the choice is only
// between flattening on the way in and flattening on the way out. On the way
// in wins twice over — a reader that flattens what it finds has to walk an
// arbitrarily deep tree to do it, on every read for ever, and any reader that
// forgets lets the depth quietly come back.
//
// Requiring the parent to be in the same channel is not a formality. A reply
// whose parent is elsewhere belongs to a conversation its own readers cannot
// see, and it is a way to hang a message off a channel you are not in.
func (s *Service) threadRootFor(ctx context.Context, channelID, parentID uuid.UUID) (uuid.UUID, error) {
	var root uuid.UUID
	// Deliberately no deleted_at filter: a message is soft-deleted and keeps
	// its place precisely so its replies still have a parent, and a thread
	// people are in the middle of does not close because the question was
	// withdrawn.
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(m.reply_to, m.id)
		FROM messages m
		WHERE m.id = $1 AND m.channel_id = $2
	`, parentID, channelID).Scan(&root)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, apperrors.New(apperrors.CodeBadRequest,
				"the message you are replying to is not in this channel")
		}
		return uuid.Nil, fmt.Errorf("resolve thread root: %w", err)
	}
	return root, nil
}

func (s *Service) createMessage(ctx context.Context, msg *Message) error {
	msg.Content = sentinelStripper.Replace(msg.Content)
	// Settled before the insert rather than fixed up afterwards, so a reply
	// with an impossible parent is never written at all.
	if msg.ReplyTo != nil {
		root, err := s.threadRootFor(ctx, msg.ChannelID, *msg.ReplyTo)
		if err != nil {
			return err
		}
		msg.ReplyTo = &root
	}
	// The channel row is read here anyway, to derive org_id rather than trust
	// it from the caller. Its type and name come back with it, so the question
	// "is this an announcement?" costs nothing — it used to be a second round
	// trip taken on every single message, which for around 95% of them found
	// nothing and threw the answer away.
	var orgID uuid.UUID
	var channelKind, channelName string
	err := s.db.QueryRow(ctx, `
		WITH target AS (
			SELECT c.org_id, c.type, COALESCE(c.name, 'Announcements') AS name
			FROM channels c WHERE c.id = $2 AND c.deleted_at IS NULL
		), inserted AS (
			INSERT INTO messages (id, org_id, channel_id, user_id, content, reply_to, kind, created_at)
			SELECT $1, t.org_id, $2, $3, $4, $5, $7, $6 FROM target t
			RETURNING created_at
		)
		SELECT inserted.created_at, target.org_id, target.type, target.name
		FROM inserted, target
	`, msg.ID, msg.ChannelID, msg.UserID, msg.Content, msg.ReplyTo, msg.CreatedAt,
		defaultKind(msg.Kind)).Scan(&msg.CreatedAt, &orgID, &channelKind, &channelName)
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
	// Anywhere else there is nobody extra to tell, and asking costs a query.
	if channelKind == "announcement" {
		s.notifyAnnouncement(ctx, msg, orgID, channelName)
	}
	return nil
}

// updateMessage returns the text as stored, which is not quite what was asked
// for, so that anything shown live matches what a reload shows.
func (s *Service) updateMessage(ctx context.Context, id uuid.UUID, content string, editedAt *time.Time) (string, error) {
	var stored string
	err := s.db.QueryRow(ctx, `
		UPDATE messages SET content = $2, edited_at = $3
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING content
	`, id, sentinelStripper.Replace(content), editedAt).Scan(&stored)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", apperrors.ErrNotFound
		}
		return "", fmt.Errorf("update message: %w", err)
	}
	return stored, nil
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
// updateLastRead advances the marker and reports the window it moved through.
//
// The previous value comes from a CTE, which sees the row as it was at the
// start of the statement, so one round trip yields both ends of the window.
// The caller needs both: a tick is a count of readers, and knowing only where
// the marker landed is not enough to add one to the right messages without
// adding it again the next time the same person reads.
//
// advanced is false when the marker did not actually move — the row was not
// there, the message was not in this channel, or this person had already read
// past it. None of those are errors, and none of them are worth announcing.
func (s *Service) updateLastRead(
	ctx context.Context, channelID, userID uuid.UUID, messageID uuid.UUID,
) (from *time.Time, to time.Time, advanced bool, err error) {
	var before *time.Time
	var after time.Time
	row := s.db.QueryRow(ctx, `
		WITH prev AS (
			SELECT cm.user_id, cm.last_read_at
			FROM channel_members cm
			WHERE cm.channel_id = $1 AND cm.user_id = $2
		)
		UPDATE channel_members cm
		SET last_read_at = GREATEST(COALESCE(cm.last_read_at, 'epoch'::timestamptz), m.created_at)
		FROM messages m, prev
		WHERE m.id = $3 AND cm.channel_id = $1 AND cm.user_id = $2
		  -- Without this a message id from another channel could advance this
		  -- channel's read marker, permanently zeroing the unread count.
		  AND m.channel_id = cm.channel_id
		RETURNING prev.last_read_at, cm.last_read_at
	`, channelID, userID, messageID)
	switch err := row.Scan(&before, &after); {
	case errors.Is(err, pgx.ErrNoRows):
		// Not a member of this channel, or the message belongs to another one.
		return nil, time.Time{}, false, nil
	case err != nil:
		return nil, time.Time{}, false, fmt.Errorf("update last read: %w", err)
	}
	return before, after, before == nil || after.After(*before), nil
}

// announceRead tells the channel that somebody's read marker has moved, so a
// sender watching their own message sees its tick change without reloading.
// The reader is excluded: it is their own action, and their client has already
// shown it.
func (s *Service) announceRead(channelID, userID uuid.UUID, from *time.Time, to time.Time) {
	if s.broadcaster == nil {
		return
	}
	s.broadcaster.BroadcastToChannel(
		channelID.String(),
		protocol.NewMessage(protocol.TypeChatReadReceipt, protocol.ChatReadReceiptPayload{
			ChannelID: channelID.String(),
			UserID:    userID.String(),
			From:      from,
			To:        to,
		}),
		userID.String(),
	)
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
		Kind:        defaultKind(msg.Kind),
		ReplyTo:     replyToString(msg.ReplyTo),
		CreatedAt:   msg.CreatedAt,
		EditedAt:    msg.EditedAt,
		DeletedAt:   msg.DeletedAt,
	}
}

// defaultKind keeps ordinary messages as they were when nothing says otherwise.
func defaultKind(kind string) string {
	if kind == "" {
		return "user"
	}
	return kind
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
		       -- Thread replies deliberately do not count. The read marker is a
		       -- point in time advanced from the channel timeline, and a reply
		       -- is not on it — so a counted reply would raise a badge that
		       -- reading the channel could never clear, and it would sit there
		       -- for ever pointing at nothing. What says a thread moved on is
		       -- the reply count on its root, which is in the timeline.
		       (SELECT COUNT(*) FROM messages msg
		          WHERE msg.channel_id = c.id AND msg.deleted_at IS NULL
		            AND msg.reply_to IS NULL
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
	if isOpenToOrg(channelType) {
		members, err := s.ChannelMemberIDs(ctx, c.ID.String())
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not announce new channel")
			return &c, nil
		}
		s.announceAdded(c.ID, members...)
	}
	return &c, nil
}

// announceAdded tells a channel that people have just joined it, which is the
// only way they find out: their sidebar is a cached list. The hub reads the
// membership as it delivers, so the people named here are reached too.
func (s *Service) announceAdded(channelID uuid.UUID, userIDs ...string) {
	if s.broadcaster == nil {
		return
	}
	s.broadcaster.BroadcastToChannel(channelID.String(), protocol.NewMessage(protocol.TypeChatMembership, protocol.ChatMembershipChangedPayload{
		ChannelID: channelID.String(),
		UserIDs:   userIDs,
		Action:    "added",
	}), "")
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
	s.announceAdded(c.ID, userID.String(), otherID.String())
	return &c, nil
}

// RecordCallEvent writes a note into the conversation between two people —
// "missed call" and the like. It is not something either of them said, so it
// is marked as an event and shown differently.
//
// Posted into their direct message, creating it if they have never spoken:
// finding out you were called belongs where you would look anyway, not in a
// separate list of calls nobody opens.
//
// Returns the conversation it was written into, so whoever was calling can be
// taken there — a call that did not happen usually turns into a message.
func (s *Service) RecordCallEvent(ctx context.Context, orgID, callerID, calleeID uuid.UUID, text string) (uuid.UUID, error) {
	dm, err := s.OpenDirectMessage(ctx, orgID, callerID, calleeID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("find the conversation: %w", err)
	}

	msg := &Message{
		ID:        uuid.New(),
		ChannelID: dm.ID,
		UserID:    callerID,
		Content:   text,
		Kind:      "event",
		Reactions: map[string][]string{},
		CreatedAt: time.Now(),
	}
	if err := s.createMessage(ctx, msg); err != nil {
		return uuid.Nil, err
	}
	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(dm.ID.String(), protocol.NewMessage(
			protocol.TypeChatMessage, protocol.ChatMessagePayload{Message: s.messageToData(msg)},
		), "")
	}
	return dm.ID, nil
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
		return nil
	}
	s.announceAdded(channelID, userID.String())
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

// notifyAnnouncement tells everybody in an announcement channel that something
// was posted. Announcements are the one kind of message that is meant to reach
// people who are not looking at the channel.
func (s *Service) notifyAnnouncement(ctx context.Context, msg *Message, orgID uuid.UUID, channelName string) {
	if s.notifier == nil {
		return
	}
	// The caller has already established that this is an announcement channel,
	// and knows the organization and the channel's name, so all that is left to
	// ask is who is in it.
	rows, err := s.db.Query(ctx, `
		SELECT cm.user_id FROM channel_members cm
		WHERE cm.channel_id = $1 AND cm.user_id <> $2
	`, msg.ChannelID, msg.UserID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("announcement recipient lookup failed")
		return
	}
	defer rows.Close()

	recipients := []uuid.UUID{}
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
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
//
// The cursor is (created_at, id), not created_at alone. created_at defaults to
// NOW(), which is the TRANSACTION timestamp — every row written in one
// transaction shares it exactly. With a bare `created_at < $2` a page boundary
// landing inside such a group silently skips every remaining message that
// shares the timestamp, and the reader is never told history went missing.
// The id tiebreaker makes the cursor total, so no row can fall between pages.
// Replies are deliberately absent. A threaded reply belongs in its thread, not
// in the middle of the channel, and leaving it inline is what made threading
// invisible: the relationship was recorded and then rendered away. What the
// timeline carries instead is the reply count on the parent, which is the way
// into the thread — see attachReplyCounts.
func (s *Service) GetMessages(ctx context.Context, channelID uuid.UUID, limit int, before *time.Time, beforeID *uuid.UUID) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+messageColumns+`
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.channel_id = $1
		  AND m.reply_to IS NULL
		  AND ($2::timestamptz IS NULL
		       OR (m.created_at, m.id) < ($2, COALESCE($4, '00000000-0000-0000-0000-000000000000'::uuid)))
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $3
	`, channelID, before, limit, beforeID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}

	// Queried newest-first for the LIMIT; the client renders oldest-first.
	reverse(messages)
	return s.hydrate(ctx, channelID, messages)
}

// messageColumns is the shape every message read shares, so the timeline and
// the thread view cannot drift into returning different things.
const messageColumns = `m.id, m.channel_id, m.user_id, m.content, m.reply_to,
	       m.created_at, m.edited_at, m.deleted_at,
	       u.name, COALESCE(u.avatar_url, ''), COALESCE(m.kind, 'user')`

func scanMessages(rows pgx.Rows) ([]Message, error) {
	defer rows.Close()

	messages := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Content, &m.ReplyTo,
			&m.CreatedAt, &m.EditedAt, &m.DeletedAt, &m.UserName, &m.UserAvatar, &m.Kind); err != nil {
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
	return messages, rows.Err()
}

func reverse(messages []Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}

// hydrate fills in everything that hangs off a page of messages, each in one
// query for the whole page. Shared by the timeline and the thread view: a
// reply that lost its reactions or its attachments on the way into a thread
// would be the same class of defect as not being able to read it at all.
func (s *Service) hydrate(ctx context.Context, channelID uuid.UUID, messages []Message) ([]Message, error) {
	withReactions, err := s.attachReactions(ctx, messages)
	if err != nil {
		return nil, err
	}
	withFiles, err := s.attachFiles(ctx, withReactions)
	if err != nil {
		return nil, err
	}
	withReads, err := s.attachReadCounts(ctx, channelID, withFiles)
	if err != nil {
		return nil, err
	}
	return s.attachReplyCounts(ctx, withReads)
}

// attachReplyCounts tells each message how big a thread hangs off it, for the
// whole page in one query rather than one query per message.
//
// A deleted reply is counted. It keeps its place in the thread exactly as a
// deleted message keeps its place in the channel, so leaving it out would open
// "2 replies" onto three things.
func (s *Service) attachReplyCounts(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	index := make(map[uuid.UUID]int, len(messages))
	for i, m := range messages {
		index[m.ID] = i
	}

	rows, err := s.db.Query(ctx, replyCountSQL, messageIDs(messages))
	if err != nil {
		return nil, fmt.Errorf("count replies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rootID uuid.UUID
		var count int
		var last time.Time
		if err := rows.Scan(&rootID, &count, &last); err != nil {
			return nil, fmt.Errorf("scan reply count: %w", err)
		}
		if i, ok := index[rootID]; ok {
			messages[i].ReplyCount = count
			at := last
			messages[i].LastReplyAt = &at
		}
	}
	return messages, rows.Err()
}

// threadReplyLimit caps a single thread read. A thread is one conversation
// about one message, so in practice it is bounded by that; the cap is here so
// a pathological one cannot be used to pull the channel's whole history in a
// single request. Past it the newest replies are returned, matching the
// timeline's own "newest page first" behaviour, and the root's reply count
// still reports the true total so the gap is visible rather than silent.
const threadReplyLimit = 200

// ThreadOf resolves any message in a thread to the thread itself.
//
// Separate from GetThread so a caller can decide whether the reader is allowed
// into the channel before any message body is loaded. Org-scoped, so an id
// from another tenant reads as not-found rather than leaking the row.
func (s *Service) ThreadOf(ctx context.Context, orgID, messageID uuid.UUID) (rootID, channelID uuid.UUID, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COALESCE(m.reply_to, m.id), m.channel_id
		FROM messages m
		JOIN channels c ON c.id = m.channel_id
		WHERE m.id = $1 AND c.org_id = $2 AND c.deleted_at IS NULL
	`, messageID, orgID).Scan(&rootID, &channelID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, uuid.Nil, apperrors.ErrNotFound
		}
		return uuid.Nil, uuid.Nil, fmt.Errorf("resolve thread: %w", err)
	}
	return rootID, channelID, nil
}

// GetThread reads one thread: the message it started from, and the replies to
// it. Any message in the thread identifies it, because a thread is opened as
// often from a reply — from search, from a notification, from a link — as from
// the parent.
//
// Org-scoped in its own right rather than trusting the caller to have checked.
// Channel membership is the caller's to enforce, since only the HTTP layer
// knows who is asking; see the thread handler.
func (s *Service) GetThread(ctx context.Context, orgID, messageID uuid.UUID) (*Thread, error) {
	rootID, channelID, err := s.ThreadOf(ctx, orgID, messageID)
	if err != nil {
		return nil, err
	}

	// The root and its replies are read separately so a long thread cannot
	// push the root itself past the limit — the root is the oldest message in
	// the thread, and so the first thing a newest-first cap would drop.
	rootRows, err := s.db.Query(ctx, `
		SELECT `+messageColumns+`
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.id = $1
	`, rootID)
	if err != nil {
		return nil, fmt.Errorf("read thread root: %w", err)
	}
	roots, err := scanMessages(rootRows)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, apperrors.ErrNotFound
	}

	replyRows, err := s.db.Query(ctx, `
		SELECT `+messageColumns+`
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.reply_to = $1
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $2
	`, rootID, threadReplyLimit)
	if err != nil {
		return nil, fmt.Errorf("read thread replies: %w", err)
	}
	replies, err := scanMessages(replyRows)
	if err != nil {
		return nil, err
	}
	reverse(replies)

	// Hydrated as one page so the whole thread costs the same number of
	// queries as a single message would.
	hydrated, err := s.hydrate(ctx, channelID, append(roots, replies...))
	if err != nil {
		return nil, err
	}
	return &Thread{
		ChannelID: channelID,
		Root:      hydrated[0],
		Replies:   hydrated[1:],
	}, nil
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
	// The insert reports the channel it landed in, and reports nothing at all
	// when it changed nothing — no such message, not a member of its channel,
	// or the same reaction twice. That is what decides whether there is
	// anything to announce, so a duplicate stays the silent no-op it was.
	var channelID uuid.UUID
	err := s.db.QueryRow(ctx, `
		WITH added AS (
			INSERT INTO message_reactions (message_id, user_id, emoji, created_at)
			SELECT m.id, cm.user_id, $3, NOW()
			FROM messages m JOIN channel_members cm ON cm.channel_id = m.channel_id
			WHERE m.id = $1 AND cm.user_id = $2 AND m.deleted_at IS NULL
			ON CONFLICT DO NOTHING
			RETURNING message_id
		)
		SELECT m.channel_id FROM added JOIN messages m ON m.id = added.message_id
	`, messageID, userID, emoji).Scan(&channelID)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("add reaction: %w", err)
	}
	s.broadcastReaction(channelID, messageID, userID, emoji, reactionAdded)
	return nil
}

func (s *Service) RemoveReaction(ctx context.Context, messageID, userID uuid.UUID, emoji string) error {
	var channelID uuid.UUID
	err := s.db.QueryRow(ctx, `
		WITH removed AS (
			DELETE FROM message_reactions
			WHERE message_id = $1 AND user_id = $2 AND emoji = $3
			RETURNING message_id
		)
		SELECT m.channel_id FROM removed JOIN messages m ON m.id = removed.message_id
	`, messageID, userID, emoji).Scan(&channelID)
	if err == pgx.ErrNoRows {
		// There was no such reaction, so nothing changed and nobody's view of
		// the message is out of date.
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove reaction: %w", err)
	}
	s.broadcastReaction(channelID, messageID, userID, emoji, reactionRemoved)
	return nil
}

// Which way a reaction went. One frame carries both directions, because a
// client applies them to the same place in the same way.
const (
	reactionAdded   = "add"
	reactionRemoved = "remove"
)

// broadcastReaction puts a reaction on everybody else's screen while the
// conversation is still happening.
//
// Storing it and returning was the whole defect: the reaction was correct in
// the database and in history, and invisible to the person you were reacting
// to until they refetched. The payload carries enough — which message, which
// emoji, who, and which way — for a client to apply it in place without
// asking for the message again.
func (s *Service) broadcastReaction(channelID, messageID, userID uuid.UUID, emoji, action string) {
	if s.broadcaster == nil {
		return
	}
	// Excluded like the sender of a message is: the client that just reacted
	// already knows, and showing it its own reaction twice is a flicker.
	s.broadcaster.BroadcastToChannel(channelID.String(), protocol.NewMessage(
		protocol.TypeChatReaction, protocol.ChatReactionPayload{
			MessageID: messageID.String(),
			Emoji:     emoji,
			Action:    action,
			UserID:    userID.String(),
		},
	), userID.String())
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
	// Off the request path: this reads the roster and everybody's name, none of
	// which the sender is waiting for. Detached from the request context so
	// finishing the response does not cancel delivery.
	go func() {
		pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		s.pushToChannel(pushCtx, channelID, userID, msg)
	}()
	return msg, nil
}

// pushSummary is as much of a message as belongs on a lock screen: enough to
// know whether it is worth opening, and no more.
const pushSummary = 140

// pushToChannel taps everybody else in the conversation on the shoulder.
//
// Reaching somebody whose browser is closed is the whole point — a message that
// only arrives if you happen to be looking is not a message, it is a page you
// have to remember to check. The worker suppresses it for anybody who is
// actually looking at the conversation, which is why this does not try to work
// out who that is.
func (s *Service) pushToChannel(ctx context.Context, channelID, senderID uuid.UUID, msg *Message) {
	if s.notifier == nil {
		return
	}

	var channelName, channelKind, senderName string
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(c.name, ''), c.type, COALESCE(u.name, u.email, '')
		FROM channels c, users u
		WHERE c.id = $1 AND u.id = $2
	`, channelID, senderID).Scan(&channelName, &channelKind, &senderName); err != nil {
		s.logger.Debug().Err(err).Msg("could not describe a message for notifying")
		return
	}

	members, err := s.ChannelMemberIDs(ctx, channelID.String())
	if err != nil {
		s.logger.Debug().Err(err).Msg("could not work out who to notify about a message")
		return
	}
	recipients := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		id, err := uuid.Parse(m)
		if err != nil || id == senderID {
			continue
		}
		recipients = append(recipients, id)
	}
	if len(recipients) == 0 {
		return
	}

	// A file on its own is a message with nothing to quote.
	body := strings.TrimSpace(msg.Content)
	if body == "" {
		body = "Sent a file"
	}
	if len([]rune(body)) > pushSummary {
		body = string([]rune(body)[:pushSummary]) + "…"
	}

	// In a direct message the person is the conversation; in a channel the
	// channel is, and the person has to be said.
	title := senderName
	if channelKind != "dm" {
		title = "#" + channelName
		body = senderName + ": " + body
	}

	s.notifier.PushToUsers(ctx, recipients, protocol.Push{
		Title: title,
		Body:  body,
		// One notification per conversation, replaced as it goes on, rather
		// than a stack of them. Renotify so a replacement is still noticed.
		Tag:      "chat-" + channelID.String(),
		Renotify: true,
		Data: map[string]any{
			"kind":      "chat.message",
			"url":       "/chat/" + channelID.String(),
			"channelId": channelID.String(),
		},
	})
}

// EditMessage and DeleteMessage enforce authorship (or channel moderation)
// before touching the row.
// EditWindow is how long a message stays editable. A conversation people have
// already read should not change underneath them, so corrections are for typos
// noticed straight away rather than for rewriting history.
const EditWindow = 5 * time.Minute

func (s *Service) EditMessage(ctx context.Context, messageID, userID uuid.UUID, content string) error {
	var owner, channelID uuid.UUID
	var createdAt time.Time
	var deletedAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT user_id, channel_id, created_at, deleted_at FROM messages WHERE id = $1`, messageID).
		Scan(&owner, &channelID, &createdAt, &deletedAt)
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
	stored, err := s.updateMessage(ctx, messageID, content, &now)
	if err != nil {
		return err
	}

	// An edit travels as a partial chat.message: no author, no createdAt. That
	// missing author is how a client tells it from a new message.
	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(channelID.String(), protocol.NewMessage(protocol.TypeChatMessage, protocol.ChatMessagePayload{
			Message: protocol.MessageData{
				ID:        messageID.String(),
				ChannelID: channelID.String(),
				Content:   stored,
				EditedAt:  &now,
			},
		}), userID.String())
	}
	return nil
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
	if err := s.deleteMessage(ctx, messageID, &now); err != nil {
		return err
	}

	if s.broadcaster != nil {
		channelID, err := s.messageChannel(ctx, messageID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not resolve channel for deleted message")
			return nil
		}
		s.broadcaster.BroadcastToChannel(channelID.String(), protocol.NewMessage(protocol.TypeChatDelete, protocol.ChatDeletePayload{
			MessageID: messageID.String(),
		}), userID.String())
	}
	return nil
}

// MarkRead advances the caller's read marker to the given message, and tells
// the channel it moved.
func (s *Service) MarkRead(ctx context.Context, channelID, userID, messageID uuid.UUID) error {
	from, to, advanced, err := s.updateLastRead(ctx, channelID, userID, messageID)
	if err != nil {
		return err
	}
	if advanced {
		s.announceRead(channelID, userID, from, to)
	}
	return nil
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

// searchSimilarityFloor is how close an approximate match has to be to count.
// Unchanged: what changed is only how the same set of rows is found.
const searchSimilarityFloor = 0.4

// searchTrigramThreshold is what pg_trgm.word_similarity_threshold is set to
// for the search query, and it sits deliberately just below the floor above.
//
// `word_similarity($2, m.content) >= 0.4` is a comparison of a function's
// result, and pg_trgm can only accelerate its own operators — so that branch
// was unindexable, and one unindexable branch in an OR forces the whole chain
// to a filter over every message in the table. Measured at 100k messages: a
// scan, ~1.2 s, growing linearly.
//
// The operator form `m.content %> $2` is indexable, but it means
// "word_similarity > threshold" rather than ">= 0.4". Setting the threshold
// fractionally under the floor makes the operator select a superset of what
// the old predicate did, and keeping the original comparison alongside it
// narrows that back to exactly the same rows: the index picks the candidates,
// the comparison decides. Postgres can then BitmapOr all three branches.
const searchTrigramThreshold = 0.39

// searchFuzzyPredicate is the approximate branch of the OR chain, named so a
// test can ask the planner what it does with it rather than take indexability
// on trust. `%>` is what the GIN trigram index can answer; the comparison
// beside it is what decides, and is the same one this query has always used.
var searchFuzzyPredicate = `(m.content %> $2 AND word_similarity($2, m.content) >= ` +
	fmt.Sprint(searchSimilarityFloor) + `)`

// searchMessagesSQL is the search itself.
var searchMessagesSQL = `
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
		        OR ` + searchFuzzyPredicate + `
		      )
		  AND ($4::uuid IS NULL OR m.channel_id = $4)
		  AND ($5::uuid IS NULL OR m.user_id = $5)
		ORDER BY (m.content_search @@ websearch_to_tsquery('english', $2)) DESC,
		         ts_rank_cd(m.content_search, websearch_to_tsquery('english', $2), 32) DESC,
		         word_similarity($2, m.content) DESC,
		         m.created_at DESC
		LIMIT $6 OFFSET $7`

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
	//
	// In a transaction because the trigram operator reads its threshold from a
	// setting, and the setting has to be this query's rather than the
	// connection's — a pooled connection is handed to whatever runs next.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		fmt.Sprintf("SET LOCAL pg_trgm.word_similarity_threshold = %v", searchTrigramThreshold)); err != nil {
		return nil, fmt.Errorf("set search similarity threshold: %w", err)
	}

	rows, err := tx.Query(ctx, searchMessagesSQL,
		orgID, text, userID, q.ChannelID, q.UserID, q.Limit, q.Offset)
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
