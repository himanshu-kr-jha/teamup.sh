package calls

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
	"github.com/teamup/teamup/pkg/protocol"
)

// knockCooldown is how soon somebody turned away may ask again.
const knockCooldown = time.Minute

// KnockRequest is somebody asking to join a call they are not a member of.
type KnockRequest struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"userId"`
	UserName  string    `json:"userName"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// tooSoonError is a knock inside the cooldown after a denial.
type tooSoonError struct{ retryAfter time.Duration }

func (e tooSoonError) Error() string { return "you were turned away a moment ago" }

// Knock asks to join. A repeat while one is waiting returns the same request, so a
// double click is not two pings.
func (s *Service) Knock(ctx context.Context, orgID, callID, userID uuid.UUID) (*KnockRequest, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	if call.Status == "ended" || call.Status == "cancelled" {
		return nil, apperrors.New(apperrors.CodeConflict, "this call is over")
	}
	mayJoin, err := s.MayJoin(ctx, orgID, callID, userID)
	if err != nil {
		return nil, err
	}
	if mayJoin {
		return nil, apperrors.New(apperrors.CodeConflict, "you can already join this call")
	}

	var deniedAt *time.Time
	if err := s.db.QueryRow(ctx, `
		SELECT MAX(answered_at) FROM call_knock_requests
		WHERE call_id = $1 AND user_id = $2 AND status = 'denied'
	`, callID, userID).Scan(&deniedAt); err != nil {
		return nil, fmt.Errorf("check last denial: %w", err)
	}
	now := s.clock()
	if deniedAt != nil {
		if wait := knockCooldown - now.Sub(*deniedAt); wait > 0 {
			return nil, tooSoonError{retryAfter: wait}
		}
	}

	req := &KnockRequest{UserID: userID, Status: "pending", UserName: s.DisplayName(ctx, userID), CreatedAt: now}
	created := false
	err = s.inCallTx(ctx, callID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO call_knock_requests (call_id, user_id, created_at) VALUES ($1, $2, $3)
			ON CONFLICT (call_id, user_id) WHERE status = 'pending' DO NOTHING
			RETURNING id
		`, callID, userID, now).Scan(&req.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return tx.QueryRow(ctx, `
				SELECT id, created_at FROM call_knock_requests
				WHERE call_id = $1 AND user_id = $2 AND status = 'pending'
			`, callID, userID).Scan(&req.ID, &req.CreatedAt)
		}
		if err != nil {
			return fmt.Errorf("record knock: %w", err)
		}
		created = true
		_, err = tx.Exec(ctx, insertEvent, callID, "knocked", userID, userID, now, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	if created {
		s.announceKnock(ctx, call, req)
	}
	return req, nil
}

// connectedMembers is who a webhook says is in the call right now.
func (s *Service) connectedMembers(ctx context.Context, callID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT user_id FROM call_sessions
		WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("list connected members: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// announceKnock pings whoever is in the call. When nobody is, the organizer and
// invited people get a notification, and the frame too, so that an open tab can
// show it. That is also what keeps a knock answerable if webhooks stop arriving
// and nobody looks connected: the people on the call still hear about it.
func (s *Service) announceKnock(ctx context.Context, call *CallModel, req *KnockRequest) {
	connected, err := s.connectedMembers(ctx, call.ID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not tell who is in a call; treating it as nobody")
	}
	payload := protocol.CallKnockPayload{
		CallID: call.ID.String(), RequestID: req.ID.String(),
		UserID: req.UserID.String(), UserName: req.UserName, Connected: true,
	}
	if len(connected) > 0 {
		s.send(connected, req.UserID, protocol.TypeCallKnock, payload)
		return
	}

	payload.Connected = false
	members, err := s.Participants(ctx, call.ID, req.UserID.String())
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not list who to tell about a knock")
		return
	}
	title := req.UserName + " asks to join a call"
	for _, raw := range members {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		s.send([]uuid.UUID{id}, req.UserID, protocol.TypeCallKnock, payload)
		if s.notifier == nil {
			continue
		}
		if err := s.notifier.Notify(ctx, call.OrgID, id, "call.knock", title, "They are waiting to be let in.",
			map[string]any{"callId": call.ID.String(), "requestId": req.ID.String(), "url": "/calls/" + call.ID.String()}); err != nil {
			s.logger.Warn().Err(err).Msg("could not notify about a knock")
		}
	}
}

func (s *Service) send(to []uuid.UUID, except uuid.UUID, kind string, payload any) {
	if s.broadcaster == nil {
		return
	}
	msg := protocol.NewMessage(kind, payload)
	for _, id := range to {
		if id != except {
			s.broadcaster.BroadcastToUser(id.String(), msg)
		}
	}
}

// ListKnockRequests is for members of the call, newest first.
func (s *Service) ListKnockRequests(ctx context.Context, orgID, callID, userID uuid.UUID) ([]KnockRequest, error) {
	if err := s.requireMember(ctx, orgID, callID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.user_id, u.name, r.status, r.created_at
		FROM call_knock_requests r JOIN users u ON u.id = r.user_id
		WHERE r.call_id = $1 ORDER BY r.created_at DESC, r.id
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("list knocks: %w", err)
	}
	defer rows.Close()
	requests := []KnockRequest{}
	for rows.Next() {
		var r KnockRequest
		if err := rows.Scan(&r.ID, &r.UserID, &r.UserName, &r.Status, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan knock: %w", err)
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

// requireMember refuses anybody but a member, with not-found for another
// organization's call. Admins and managers get no exception: reading a log is not
// the same as deciding who walks in.
func (s *Service) requireMember(ctx context.Context, orgID, callID, userID uuid.UUID) error {
	if _, err := s.GetCall(ctx, orgID, callID); err != nil {
		return err
	}
	member, err := s.IsMember(ctx, orgID, callID, userID)
	if err != nil {
		return err
	}
	if !member {
		return apperrors.New(apperrors.CodeForbidden, "only members of this call can see who is at the door")
	}
	return nil
}

// AnswerKnock admits or denies. The update is what decides it, so two members
// answering at once produce one answer and the other is told so.
func (s *Service) AnswerKnock(ctx context.Context, orgID, callID, requestID, actorID uuid.UUID, admit bool) error {
	if err := s.requireMember(ctx, orgID, callID, actorID); err != nil {
		return err
	}
	status := "denied"
	if admit {
		status = "admitted"
	}
	now := s.clock()
	var knocker uuid.UUID
	err := s.inCallTx(ctx, callID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			UPDATE call_knock_requests SET status = $3, answered_by = $4, answered_at = $5
			WHERE id = $1 AND call_id = $2 AND status = 'pending'
			RETURNING user_id
		`, requestID, callID, status, actorID, now).Scan(&knocker)
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM call_knock_requests WHERE id = $1 AND call_id = $2)`,
				requestID, callID).Scan(&exists); err != nil {
				return fmt.Errorf("find knock: %w", err)
			}
			if !exists {
				return apperrors.ErrNotFound
			}
			return apperrors.New(apperrors.CodeConflict, "this request has already been answered")
		}
		if err != nil {
			return fmt.Errorf("answer knock: %w", err)
		}
		if admit {
			// Becoming a member is what stops them knocking again on reconnect.
			if _, err := tx.Exec(ctx, `
				INSERT INTO call_participants (call_id, user_id, role) VALUES ($1, $2, 'participant')
				ON CONFLICT (call_id, user_id) DO NOTHING
			`, callID, knocker); err != nil {
				return fmt.Errorf("admit: %w", err)
			}
		}
		_, err = tx.Exec(ctx, insertEvent, callID, status, knocker, actorID, now, nil)
		return err
	})
	if err != nil {
		return err
	}

	s.send([]uuid.UUID{knocker}, uuid.Nil, protocol.TypeCallKnockResult, protocol.CallKnockResultPayload{
		CallID: callID.String(), RequestID: requestID.String(), Status: status,
	})
	// Everybody who could have been prompted, whichever way they were reached. That
	// includes whoever answered, whose other tabs and devices are showing it too.
	resolved := protocol.CallKnockResolvedPayload{
		CallID: callID.String(), RequestID: requestID.String(), Status: status, AnsweredBy: actorID.String(),
	}
	if others, err := s.promptedMembers(ctx, callID); err != nil {
		s.logger.Warn().Err(err).Msg("could not clear a knock prompt")
	} else {
		s.send(others, knocker, protocol.TypeCallKnockResolved, resolved)
	}
	return nil
}

// promptedMembers is the organizer, invitees and anybody connected.
func (s *Service) promptedMembers(ctx context.Context, callID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id FROM call_participants WHERE call_id = $1
		UNION
		SELECT user_id FROM call_sessions WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("list prompted members: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// endPendingKnocks closes whatever is waiting when a call ends, and tells whoever
// is standing at the door.
func (s *Service) endPendingKnocks(ctx context.Context, callID uuid.UUID) {
	rows, err := s.db.Query(ctx, `
		UPDATE call_knock_requests SET status = 'ended', answered_at = $2
		WHERE call_id = $1 AND status = 'pending'
		RETURNING id, user_id
	`, callID, s.clock())
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not end pending knocks")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var requestID, knocker uuid.UUID
		if err := rows.Scan(&requestID, &knocker); err != nil {
			s.logger.Warn().Err(err).Msg("could not read an ended knock")
			continue
		}
		s.send([]uuid.UUID{knocker}, uuid.Nil, protocol.TypeCallKnockResult, protocol.CallKnockResultPayload{
			CallID: callID.String(), RequestID: requestID.String(), Status: "ended",
		})
	}
}

func (h *Handler) Knock(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, err := h.service.Knock(c.Request.Context(), orgID, id, userID)
	var tooSoon tooSoonError
	if errors.As(err, &tooSoon) {
		seconds := int(math.Ceil(tooSoon.retryAfter.Seconds()))
		c.Header("Retry-After", strconv.Itoa(seconds))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": tooSoon.Error(), "retryAfterSeconds": seconds})
		return
	}
	if err != nil {
		httpx.Error(c, err, "failed to knock")
		return
	}
	c.JSON(http.StatusOK, gin.H{"request": req})
}

func (h *Handler) ListKnockRequests(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	requests, err := h.service.ListKnockRequests(c.Request.Context(), orgID, id, userID)
	if err != nil {
		httpx.Error(c, err, "failed to list requests")
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

func (h *Handler) AdmitKnock(c *gin.Context) { h.answerKnock(c, true) }
func (h *Handler) DenyKnock(c *gin.Context)  { h.answerKnock(c, false) }

func (h *Handler) answerKnock(c *gin.Context, admit bool) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	requestID, ok := pathID(c, "rid")
	if !ok {
		return
	}
	if err := h.service.AnswerKnock(c.Request.Context(), orgID, id, requestID, userID, admit); err != nil {
		httpx.Error(c, err, "failed to answer request")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "answered"})
}
