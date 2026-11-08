package calls

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// maxWebhookBody bounds what is read before the signature has been checked. A
// webhook is a small JSON document, and this route is public.
const maxWebhookBody = 1 << 20

// LiveKitWebhook receives what happened in a room. The signature is the only
// credential, and it covers the body: the JWT carries a SHA-256 of it, which the
// SDK checks, so nginx exposing this path gives nobody a way in. Anything refused
// records nothing.
func (h *Handler) LiveKitWebhook(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBody)
	keys := auth.NewFileBasedKeyProviderFromMap(map[string]string{
		h.service.cfg.LiveKitAPIKey: h.service.cfg.LiveKitAPISecret,
	})
	body, err := webhook.Receive(c.Request, keys)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook signature"})
		return
	}
	var event livekit.WebhookEvent
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true, AllowPartial: true}).Unmarshal(body, &event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook body"})
		return
	}
	// A failure is answered with an error so LiveKit sends it again; every step
	// is safe to repeat.
	if err := h.service.HandleWebhook(c.Request.Context(), &event); err != nil {
		httpx.Error(c, err, "failed to record webhook")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// webhookCall is what a webhook needs of the call its room belongs to.
type webhookCall struct {
	ID, OrgID, CreatedBy uuid.UUID
	Status               string
	Scheduled            bool
}

// HandleWebhook records one LiveKit event. Anything that is not about a room and a
// person of ours is ignored without an error: LiveKit retries whatever is not a
// 2xx, and egress participants and rooms made by hand are not ours to record.
func (s *Service) HandleWebhook(ctx context.Context, event *livekit.WebhookEvent) error {
	roomName := event.GetRoom().GetName()
	if roomName == "" {
		return nil
	}
	call, err := s.callForRoom(ctx, roomName)
	if err != nil || call == nil {
		return err
	}

	at := eventTime(event.GetCreatedAt(), s.clock())
	switch event.GetEvent() {
	case webhook.EventRoomFinished:
		_, err := s.finishRoom(ctx, call, at)
		return err
	case webhook.EventParticipantJoined, webhook.EventParticipantLeft,
		webhook.EventTrackPublished, webhook.EventTrackUnpublished:
	default:
		return nil
	}

	participant := event.GetParticipant()
	// A guest is not on the attendance trail; arriving is only checked.
	if strings.HasPrefix(participant.GetIdentity(), GuestIdentityPrefix) {
		if event.GetEvent() == webhook.EventParticipantJoined {
			s.guestArrived(ctx, call, roomName, participant.GetIdentity())
		}
		return nil
	}
	userID, known, err := s.personInOrg(ctx, call.OrgID, participant.GetIdentity())
	if err != nil || !known || participant.GetSid() == "" {
		return err
	}

	switch event.GetEvent() {
	case webhook.EventParticipantJoined:
		if err := s.recordJoined(ctx, call, userID, participant.GetSid(), eventTime(participant.GetJoinedAt(), at)); err != nil {
			return err
		}
		// They really connected, so the trail says so. But a token cannot be revoked,
		// so somebody who has left can still arrive with one they already had, and
		// this is the moment to see them out.
		active, err := s.isActivePerson(ctx, call.OrgID, userID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not check whether somebody who connected is active")
		} else if !active {
			s.putOut(ctx, roomName, userID)
			return nil
		}
		if s.joined != nil && call.Status != "ended" {
			s.joined(context.WithoutCancel(ctx), call.OrgID, call.ID, roomName)
		}
		return nil
	case webhook.EventParticipantLeft:
		return s.recordLeft(ctx, call, userID, participant.GetSid(), participant.GetJoinedAt(), at)
	}

	// Cameras and microphones are not on the trail.
	track := event.GetTrack()
	if track.GetSource() != livekit.TrackSource_SCREEN_SHARE || track.GetSid() == "" {
		return nil
	}
	kind := "screen_share_started"
	if event.GetEvent() == webhook.EventTrackUnpublished {
		kind = "screen_share_stopped"
	}
	_, err = s.db.Exec(ctx, insertEvent, call.ID, kind, userID, userID, at, kind+":"+track.GetSid())
	if err != nil {
		return fmt.Errorf("record screen share: %w", err)
	}
	return nil
}

// eventTime reads LiveKit's unix seconds, or falls back when it sent none.
func eventTime(seconds int64, fallback time.Time) time.Time {
	if seconds == 0 {
		return fallback
	}
	return time.Unix(seconds, 0)
}

func (s *Service) callForRoom(ctx context.Context, roomName string) (*webhookCall, error) {
	var call webhookCall
	// Room names are short and were not unique before now, so prefer the live one.
	err := s.db.QueryRow(ctx, `
		SELECT id, org_id, created_by, status, scheduled_start IS NOT NULL
		FROM calls WHERE room_name = $1
		ORDER BY status = 'ended', created_at DESC LIMIT 1
	`, roomName).Scan(&call.ID, &call.OrgID, &call.CreatedBy, &call.Status, &call.Scheduled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find call for room: %w", err)
	}
	return &call, nil
}

// personInOrg resolves a participant's identity, which is a user id for everybody
// we issued a token to.
func (s *Service) personInOrg(ctx context.Context, orgID uuid.UUID, identity string) (uuid.UUID, bool, error) {
	userID, err := uuid.Parse(identity)
	if err != nil {
		return uuid.Nil, false, nil
	}
	var known bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2)`,
		userID, orgID).Scan(&known); err != nil {
		return uuid.Nil, false, fmt.Errorf("find participant: %w", err)
	}
	return userID, known, nil
}

// insertEvent adds to the audit trail. A retried webhook has the same dedupe key
// and lands once.
const insertEvent = `
	INSERT INTO call_events (call_id, type, user_id, actor_id, at, dedupe_key)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (call_id, dedupe_key) DO NOTHING`

// inCallTx runs fn holding this call's row, so that events for one call arriving
// at two replicas at once cannot each work out call_participants from a list that
// is missing the other's session.
func (s *Service) inCallTx(ctx context.Context, callID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM calls WHERE id = $1 FOR UPDATE`, callID); err != nil {
		return fmt.Errorf("lock call: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// syncParticipant keeps call_participants true for what already reads it: their
// first join, and when they left, which stays empty while any session is open.
// Nothing here is the record. The sessions are.
func syncParticipant(ctx context.Context, tx pgx.Tx, callID, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id, role, joined_at, left_at)
		SELECT $1, $2, 'participant', MIN(joined_at),
		       CASE WHEN bool_or(left_at IS NULL) THEN NULL ELSE MAX(left_at) END
		FROM call_sessions
		WHERE call_id = $1 AND user_id = $2 AND joined_at IS NOT NULL
		HAVING COUNT(*) > 0
		ON CONFLICT (call_id, user_id) DO UPDATE
		SET joined_at = EXCLUDED.joined_at, left_at = EXCLUDED.left_at
	`, callID, userID)
	if err != nil {
		return fmt.Errorf("sync call participant: %w", err)
	}
	return nil
}

func (s *Service) recordJoined(ctx context.Context, call *webhookCall, userID uuid.UUID, sid string, at time.Time) error {
	return s.inCallTx(ctx, call.ID, func(tx pgx.Tx) error {
		var stillThere bool
		if err := tx.QueryRow(ctx, `
			INSERT INTO call_sessions (call_id, user_id, participant_sid, joined_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (call_id, participant_sid) DO UPDATE
			SET joined_at = COALESCE(call_sessions.joined_at, EXCLUDED.joined_at)
			RETURNING left_at IS NULL
		`, call.ID, userID, sid, at).Scan(&stillThere); err != nil {
			return fmt.Errorf("record session: %w", err)
		}
		if _, err := tx.Exec(ctx, insertEvent, call.ID, "joined", userID, userID, at, "joined:"+sid); err != nil {
			return fmt.Errorf("record join: %w", err)
		}
		if err := syncParticipant(ctx, tx, call.ID, userID); err != nil {
			return err
		}
		// Somebody other than the caller is really there, however they came to
		// have a token.
		if userID != call.CreatedBy {
			if _, err := tx.Exec(ctx, `UPDATE calls SET answered = TRUE WHERE id = $1`, call.ID); err != nil {
				return fmt.Errorf("record answered: %w", err)
			}
		}
		// A connection opens a meeting or occurrence that had not opened, or whose
		// room emptied a moment before the token was used. A late join for a
		// session that is already over, delivered after the room finished, does not.
		if stillThere && call.Scheduled {
			if _, err := tx.Exec(ctx, `
				UPDATE calls
				SET status = 'active', started_at = COALESCE(started_at, $2),
				    ended_at = NULL, duration_seconds = NULL, updated_at = NOW()
				WHERE id = $1 AND status IN ('scheduled', 'ended')
				  AND (ended_at IS NULL OR ended_at <= $2)
			`, call.ID, at); err != nil {
				return fmt.Errorf("open call: %w", err)
			}
		}
		return nil
	})
}

// recordLeft closes a session. A leave can overtake its join: it is held in the
// same row, and the join fills in the rest. LiveKit's leave also says when they
// joined, so a join that was lost altogether still leaves a whole session.
func (s *Service) recordLeft(ctx context.Context, call *webhookCall, userID uuid.UUID, sid string, joinedAt int64, at time.Time) error {
	var joined *time.Time
	if joinedAt != 0 {
		t := time.Unix(joinedAt, 0)
		joined = &t
	}
	return s.inCallTx(ctx, call.ID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO call_sessions (call_id, user_id, participant_sid, joined_at, left_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (call_id, participant_sid) DO UPDATE
			SET joined_at = COALESCE(call_sessions.joined_at, EXCLUDED.joined_at),
			    left_at = LEAST(call_sessions.left_at, EXCLUDED.left_at)
		`, call.ID, userID, sid, joined, at); err != nil {
			return fmt.Errorf("close session: %w", err)
		}
		if _, err := tx.Exec(ctx, insertEvent, call.ID, "left", userID, userID, at, "left:"+sid); err != nil {
			return fmt.Errorf("record leave: %w", err)
		}
		return syncParticipant(ctx, tx, call.ID, userID)
	})
}

// closeOpenSessions says everybody still connected as of a moment has gone, for
// when the room itself is over and a leave may never come. Somebody who connected
// after that moment is in a new room of the same name and stays.
func (s *Service) closeOpenSessions(ctx context.Context, callID uuid.UUID, at time.Time) error {
	return s.inCallTx(ctx, callID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE call_sessions SET left_at = $2
			WHERE call_id = $1 AND left_at IS NULL AND joined_at <= $2
			RETURNING user_id, participant_sid
		`, callID, at)
		if err != nil {
			return fmt.Errorf("close open sessions: %w", err)
		}
		defer rows.Close()
		type closed struct {
			user uuid.UUID
			sid  string
		}
		var sessions []closed
		for rows.Next() {
			var c closed
			if err := rows.Scan(&c.user, &c.sid); err != nil {
				return fmt.Errorf("scan closed session: %w", err)
			}
			sessions = append(sessions, c)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, c := range sessions {
			if _, err := tx.Exec(ctx, insertEvent, callID, "left", c.user, c.user, at, "left:"+c.sid); err != nil {
				return fmt.Errorf("record leave: %w", err)
			}
			if err := syncParticipant(ctx, tx, callID, c.user); err != nil {
				return err
			}
		}
		return nil
	})
}

// finishRoom ends the record when LiveKit says the room is gone. A meeting not yet
// opened has nothing to finish, and neither does a room that has already filled up
// again. It says whether it was what ended the call.
func (s *Service) finishRoom(ctx context.Context, call *webhookCall, at time.Time) (bool, error) {
	if err := s.closeOpenSessions(ctx, call.ID, at); err != nil {
		return false, err
	}
	if call.Status != "active" {
		return false, nil
	}
	var occupied bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM call_sessions WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL)
	`, call.ID).Scan(&occupied); err != nil {
		return false, fmt.Errorf("check room: %w", err)
	}
	if occupied {
		return false, nil
	}

	if err := s.MarkCallEnded(ctx, call.OrgID, call.ID); err != nil {
		// Somebody hanging up got there first, or the row moved on since it was read.
		if errors.Is(err, apperrors.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	// The same things hanging up on an empty room does, for when nobody could.
	loaded, err := s.GetCall(ctx, call.OrgID, call.ID)
	if err != nil {
		return true, err
	}
	if others, err := s.Participants(ctx, call.ID, ""); err == nil {
		s.AnnounceCallOver(ctx, loaded, "ended", "", others)
	}
	s.NoteIfMissed(ctx, call.ID, "no_answer")
	return true, nil
}

// CallEvent is one line of the audit trail.
type CallEvent struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	UserID    *uuid.UUID `json:"userId"`
	UserName  string     `json:"userName"`
	ActorID   *uuid.UUID `json:"actorId"`
	ActorName string     `json:"actorName"`
	At        time.Time  `json:"at"`
}

// CallSession is one connection of one person.
type CallSession struct {
	UserID   uuid.UUID  `json:"userId"`
	UserName string     `json:"userName"`
	JoinedAt time.Time  `json:"joinedAt"`
	LeftAt   *time.Time `json:"leftAt"`
}

// CallLog is the audit trail and the sessions behind it. AttendanceRecorded is
// false when no connection was ever reported: either nobody connected or webhooks
// were not arriving, and that cannot be told apart from here, so a client says
// there is no data rather than that nobody came.
type CallLog struct {
	Events             []CallEvent   `json:"events"`
	Sessions           []CallSession `json:"sessions"`
	AttendanceRecorded bool          `json:"attendanceRecorded"`
}

// MayReadLog is decision 11: its members, an organization admin or owner, and a
// manager over at least one of its members, who then gets the whole log.
//
// A standing meeting's room is open to everybody, so being in it makes nobody a
// member, and whether others came is for attendance's rule to say.
func (s *Service) MayReadLog(ctx context.Context, orgID uuid.UUID, call *CallModel, userID uuid.UUID, isOrgAdmin bool) (bool, error) {
	if isOrgAdmin {
		return true, nil
	}
	if call.StandingMeetingID == nil {
		member, err := s.IsMember(ctx, orgID, call.ID, userID)
		if err != nil || member {
			return member, err
		}
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id FROM call_participants WHERE call_id = $1
		UNION
		SELECT cm.user_id FROM channel_members cm
		JOIN calls c ON c.channel_id = cm.channel_id WHERE c.id = $1
	`, call.ID)
	if err != nil {
		return false, fmt.Errorf("list call members: %w", err)
	}
	members, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return false, fmt.Errorf("scan call member: %w", err)
	}
	return s.chart.AuthorityOverAny(ctx, orgID, userID, members)
}

// GetCallLog reads the trail. Somebody who may not read it gets the same answer as
// for a call that does not exist.
func (s *Service) GetCallLog(ctx context.Context, orgID, callID, userID uuid.UUID, isOrgAdmin bool) (*CallLog, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	readable, err := s.MayReadLog(ctx, orgID, call, userID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	if !readable {
		return nil, apperrors.ErrNotFound
	}

	log := &CallLog{Events: []CallEvent{}, Sessions: []CallSession{}}
	rows, err := s.db.Query(ctx, `
		SELECT e.id, e.type, e.user_id, COALESCE(u.name, ''), e.actor_id, COALESCE(a.name, ''), e.at
		FROM call_events e
		LEFT JOIN users u ON u.id = e.user_id
		LEFT JOIN users a ON a.id = e.actor_id
		WHERE e.call_id = $1 ORDER BY e.at, e.created_at, e.id
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("read call events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e CallEvent
		if err := rows.Scan(&e.ID, &e.Type, &e.UserID, &e.UserName, &e.ActorID, &e.ActorName, &e.At); err != nil {
			return nil, fmt.Errorf("scan call event: %w", err)
		}
		log.Events = append(log.Events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sessions, err := s.db.Query(ctx, `
		SELECT cs.user_id, u.name, cs.joined_at, cs.left_at
		FROM call_sessions cs JOIN users u ON u.id = cs.user_id
		WHERE cs.call_id = $1 AND cs.joined_at IS NOT NULL
		ORDER BY cs.joined_at, cs.id
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("read call sessions: %w", err)
	}
	defer sessions.Close()
	for sessions.Next() {
		var cs CallSession
		if err := sessions.Scan(&cs.UserID, &cs.UserName, &cs.JoinedAt, &cs.LeftAt); err != nil {
			return nil, fmt.Errorf("scan call session: %w", err)
		}
		log.Sessions = append(log.Sessions, cs)
	}
	log.AttendanceRecorded = len(log.Sessions) > 0
	return log, sessions.Err()
}

func (h *Handler) GetCallLog(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	log, err := h.service.GetCallLog(c.Request.Context(), orgID, id, userID, isAdmin(c))
	if err != nil {
		httpx.Error(c, err, "call not found")
		return
	}
	c.JSON(http.StatusOK, log)
}

// mergedDuration is how long somebody was present across sessions, not counting
// overlapping devices twice. It takes [start, end] pairs in any order.
func mergedDuration(spans [][2]time.Time) time.Duration {
	sort.Slice(spans, func(i, j int) bool { return spans[i][0].Before(spans[j][0]) })
	var total time.Duration
	var reached time.Time
	for _, span := range spans {
		start, end := span[0], span[1]
		if start.Before(reached) {
			start = reached
		}
		if end.After(start) {
			total += end.Sub(start)
			reached = end
		}
	}
	return total
}
