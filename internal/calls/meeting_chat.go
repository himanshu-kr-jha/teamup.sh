package calls

// A meeting's own chat: what the people in a call write to each other beside
// the video, as in a video meeting's side panel. Members and guests alike write
// and read it while the meeting is on; it stays with the call afterwards, for
// whoever may read the call's log, and goes into the minutes.
//
// The server is the only one that delivers a message: it is saved, then sent to
// the room over LiveKit, so that everybody connected (guests too, who have no
// TeamUp session) gets it at once, and a page ignores chat from anybody but the
// server, so nobody can put words in somebody else's name.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// MeetingChatTopic is the LiveKit data topic a meeting's chat travels on.
const MeetingChatTopic = "teamup.meeting-chat"

const (
	// maxChatMessage is how long one message may be, in characters.
	maxChatMessage = 2000
	// chatBurst messages in chatWindow is as fast as one person may write;
	// anything faster is somebody pasting in a loop, not talking.
	chatBurst  = 10
	chatWindow = 10 * time.Second
	// maxChatHistory is the most of a meeting's chat read at once.
	maxChatHistory = 1000
)

var (
	// ErrEmptyChatMessage is a message with nothing in it.
	ErrEmptyChatMessage = apperrors.New(apperrors.CodeValidation, "write something to send")
	// ErrLongChatMessage is a message over maxChatMessage characters.
	ErrLongChatMessage = apperrors.New(apperrors.CodeValidation, fmt.Sprintf("a message can be at most %d characters", maxChatMessage))
	// ErrNotInMeeting is writing to a meeting one is not in.
	ErrNotInMeeting = apperrors.New(apperrors.CodeForbidden, "join the meeting to write in its chat")
	// ErrChatTooFast is more than chatBurst messages in chatWindow.
	ErrChatTooFast = apperrors.New(apperrors.CodeRateLimited, "you are sending messages too quickly; wait a moment")
	// ErrNoChat is a chat this person may not read: the same answer as for a
	// call that does not exist.
	ErrNoChat = apperrors.New(apperrors.CodeNotFound, "call not found")
)

// ChatMessage is one message in a meeting's chat. SenderID is the sender's
// identity in the room (a member's user id, or guest-<id>), which is what a
// page compares with its own to know which messages are its own.
type ChatMessage struct {
	ID         uuid.UUID `json:"id"`
	CallID     uuid.UUID `json:"callId"`
	SenderID   string    `json:"senderId"`
	SenderName string    `json:"senderName"`
	Guest      bool      `json:"guest"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"createdAt"`
}

// chatSender is who is writing.
type chatSender struct {
	userID  *uuid.UUID
	guestID *uuid.UUID
	name    string
}

func (c chatSender) identity() string {
	if c.guestID != nil {
		return GuestIdentityPrefix + c.guestID.String()
	}
	return c.userID.String()
}

// cleanChatMessage is the message as it is kept: trimmed, and refused when
// empty or too long.
func cleanChatMessage(content string) (string, error) {
	content = strings.TrimSpace(strings.ToValidUTF8(content, ""))
	if content == "" {
		return "", ErrEmptyChatMessage
	}
	if utf8.RuneCountInString(content) > maxChatMessage {
		return "", ErrLongChatMessage
	}
	return content, nil
}

// SendChatMessage is a member writing in the chat of a meeting they are in.
func (s *Service) SendChatMessage(ctx context.Context, orgID, callID, userID uuid.UUID, content string) (*ChatMessage, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	may, err := s.MayJoin(ctx, orgID, callID, userID)
	if err != nil {
		return nil, err
	}
	if !may {
		return nil, ErrNoChat
	}
	return s.sendChat(ctx, call, chatSender{userID: &userID, name: s.DisplayName(ctx, userID)}, content)
}

// SendGuestChatMessage is a guest writing in the chat of the meeting behind
// their link. The rejoin key they were given on joining is who they are.
func (s *Service) SendGuestChatMessage(ctx context.Context, token, rejoinKey, content string) (*ChatMessage, error) {
	call, guestID, name, err := s.guestInMeeting(ctx, token, rejoinKey)
	if err != nil {
		return nil, err
	}
	return s.sendChat(ctx, call, chatSender{guestID: &guestID, name: name}, content)
}

func (s *Service) sendChat(ctx context.Context, call *CallModel, from chatSender, content string) (*ChatMessage, error) {
	content, err := cleanChatMessage(content)
	if err != nil {
		return nil, err
	}
	if call.Status != "active" {
		return nil, ErrCallOver
	}
	// Being allowed in is not being in: the chat is for the people in the room.
	// Without a media server there is nobody to ask, and the rule above stands.
	in, err := s.mediaOf().InRoom(ctx, call.RoomName, from.identity())
	switch {
	case errors.Is(err, errNoMedia):
	case err != nil:
		return nil, fmt.Errorf("ask the media server who is in the meeting: %w", err)
	case !in:
		return nil, ErrNotInMeeting
	}

	var recent int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM call_messages
		WHERE call_id = $1 AND created_at > $2
		  AND (sender_user_id = $3 OR sender_guest_id = $4)
	`, call.ID, s.clock().Add(-chatWindow), from.userID, from.guestID).Scan(&recent); err != nil {
		return nil, fmt.Errorf("count recent chat messages: %w", err)
	}
	if recent >= chatBurst {
		return nil, ErrChatTooFast
	}

	msg := &ChatMessage{
		ID:         uuid.New(),
		CallID:     call.ID,
		SenderID:   from.identity(),
		SenderName: from.name,
		Guest:      from.guestID != nil,
		Content:    content,
		// As the database keeps it, so the message sent and the one read back agree.
		CreatedAt: s.clock().UTC().Truncate(time.Microsecond),
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO call_messages (id, call_id, sender_user_id, sender_guest_id, sender_name, content, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, msg.ID, msg.CallID, from.userID, from.guestID, truncateRunes(from.name, 120), msg.Content, msg.CreatedAt); err != nil {
		return nil, fmt.Errorf("save chat message: %w", err)
	}

	// Saved is sent: somebody whose packet is lost reads it on opening the chat.
	payload, _ := json.Marshal(msg)
	if err := s.mediaOf().SendData(ctx, call.RoomName, MeetingChatTopic, payload); err != nil && !errors.Is(err, errNoMedia) {
		s.logger.Warn().Err(err).Str("call", call.ID.String()).Msg("could not deliver a meeting chat message")
	}
	return msg, nil
}

// ChatMessages is a meeting's chat, oldest first, for a member: anybody who may
// join the meeting while it is on, and whoever may read its log afterwards.
func (s *Service) ChatMessages(ctx context.Context, orgID, callID, userID uuid.UUID, isOrgAdmin bool) ([]ChatMessage, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	may, err := s.MayReadLog(ctx, orgID, call, userID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	if !may && call.Status == "active" {
		if may, err = s.MayJoin(ctx, orgID, callID, userID); err != nil {
			return nil, err
		}
	}
	if !may {
		return nil, ErrNoChat
	}
	return s.chatOf(ctx, call.ID)
}

// GuestChatMessages is the chat of the meeting behind a guest's link, while
// they may be in it.
func (s *Service) GuestChatMessages(ctx context.Context, token, rejoinKey string) ([]ChatMessage, error) {
	call, _, _, err := s.guestInMeeting(ctx, token, rejoinKey)
	if err != nil {
		return nil, err
	}
	return s.chatOf(ctx, call.ID)
}

// guestInMeeting is the guest a rejoin key belongs to, in a meeting that is on.
// A guest who was put out, or whose link is off, has no chat.
func (s *Service) guestInMeeting(ctx context.Context, token, rejoinKey string) (*CallModel, uuid.UUID, string, error) {
	link, err := s.guestLink(ctx, token)
	if err != nil {
		return nil, uuid.Nil, "", err
	}
	if rejoinKey == "" {
		return nil, uuid.Nil, "", ErrNotInMeeting
	}
	var id uuid.UUID
	var name string
	err = s.db.QueryRow(ctx, `
		SELECT id, name FROM call_guests
		WHERE call_id = $1 AND rejoin_hash = $2 AND removed_at IS NULL
		ORDER BY created_at DESC LIMIT 1`, link.callID, hashKey(rejoinKey)).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uuid.Nil, "", ErrNotInMeeting
	}
	if err != nil {
		return nil, uuid.Nil, "", fmt.Errorf("find guest: %w", err)
	}
	call, err := s.GetCall(ctx, link.orgID, link.callID)
	if err != nil {
		return nil, uuid.Nil, "", ErrNoGuestLink
	}
	if s.guestState(call) != "open" {
		return nil, uuid.Nil, "", ErrCallOver
	}
	return call, id, name, nil
}

func (s *Service) chatOf(ctx context.Context, callID uuid.UUID) ([]ChatMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, call_id, sender_user_id, sender_guest_id, sender_name, content, created_at
		FROM (
			SELECT * FROM call_messages WHERE call_id = $1
			ORDER BY created_at DESC, id DESC LIMIT $2
		) latest
		ORDER BY created_at, id
	`, callID, maxChatHistory)
	if err != nil {
		return nil, fmt.Errorf("read meeting chat: %w", err)
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		var userID, guestID *uuid.UUID
		if err := rows.Scan(&m.ID, &m.CallID, &userID, &guestID, &m.SenderName, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("read meeting chat: %w", err)
		}
		m.CreatedAt = m.CreatedAt.UTC()
		switch {
		case guestID != nil:
			m.SenderID, m.Guest = GuestIdentityPrefix+guestID.String(), true
		case userID != nil:
			m.SenderID = userID.String()
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// GuestKeyHeader carries a guest's rejoin key, which is who they are in the
// meeting's chat. A header, not the address, so it is never in a log.
const GuestKeyHeader = "X-Guest-Key"

func (h *Handler) listChat(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	messages, err := h.service.ChatMessages(c.Request.Context(), orgID, id, userID, isAdmin(c))
	if err != nil {
		httpx.Error(c, err, "failed to read the meeting chat")
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

func (h *Handler) sendChat(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	msg, err := h.service.SendChatMessage(c.Request.Context(), orgID, id, userID, req.Content)
	if err != nil {
		httpx.Error(c, err, "failed to send the message")
		return
	}
	c.JSON(http.StatusCreated, msg)
}

func (h *Handler) listGuestChat(c *gin.Context) {
	messages, err := h.service.GuestChatMessages(c.Request.Context(), c.Param("token"), c.GetHeader(GuestKeyHeader))
	if err != nil {
		httpx.Error(c, err, "failed to read the meeting chat")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

func (h *Handler) sendGuestChat(c *gin.Context) {
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	msg, err := h.service.SendGuestChatMessage(c.Request.Context(), c.Param("token"), c.GetHeader(GuestKeyHeader), req.Content)
	if err != nil {
		httpx.Error(c, err, "failed to send the message")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, msg)
}
