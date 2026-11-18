package calls

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
	"github.com/teamup/teamup/pkg/protocol"
)

// Adding people to a call that is already going.
//
// Two people talking who want a third used to have to copy the call's link and
// send it. Instead, anybody in the call can ring colleagues into it: each gets
// the same ring a new call gives, and answering puts them in this call. How a
// ring ends is told to everybody in the call, and it never ends the call for
// the people already in it - one more person not coming changes nothing for
// the two who are talking.
//
// A ring into a call is marked on the person's call_participants row: rung_by
// and rung_at say who rang and when, and ring_ended_at when it stopped. While
// it has not stopped they are being rung.

// maxRingAtOnce bounds one request. Adding people one ring at a time is the
// point; a whole department is a meeting, and has its own way in.
const maxRingAtOnce = 20

// ringRepeatLimit is how often the same person may be rung into the same call in
// ringRepeatWindow. Ringing somebody again who did not answer is fine; ringing
// them all afternoon is not.
const (
	ringRepeatLimit  = 5
	ringRepeatWindow = 10 * time.Minute
)

var (
	// ErrNotInThisCall is ringing people into a call you are not in yourself.
	ErrNotInThisCall = apperrors.New(apperrors.CodeForbidden, "only somebody in this call can ring people into it")
	// ErrCallNotGoing is ringing people into a call that is not on.
	ErrCallNotGoing = apperrors.New(apperrors.CodeConflict, "this call is not going on")
	// ErrNotRinging is cancelling a ring that has already stopped.
	ErrNotRinging = apperrors.New(apperrors.CodeConflict, "they are not being rung")
)

// RingStoppedPrefix starts the reason a rung person's own tabs are given when
// their ring stops ("ring_cancelled", "ring_no_answer"): it takes the ring away
// and never means the call itself is over.
const RingStoppedPrefix = "ring_"

// Ring outcomes, as call.ring.result says them.
const (
	RingAnswered  = "answered"
	RingDeclined  = "declined"
	RingNoAnswer  = "no_answer"
	RingBusy      = "busy"
	RingOnLeave   = "on_leave"
	RingCancelled = "cancelled"
)

// RungPerson is one person a ring into a call went to, and where it stands.
type RungPerson struct {
	UserID uuid.UUID `json:"userId"`
	Name   string    `json:"name"`
	// Status is "ringing", or why they were not rung: "busy" or "on_leave".
	Status string `json:"status"`
	// BackOn is the day somebody on leave is back, when it is known.
	BackOn *string `json:"backOn,omitempty"`
}

// RingInto rings colleagues into a call that is going, on behalf of somebody in
// it. Everybody named must be an active member of the organization who is not
// in the call and not being rung for it already. Nobody is rung unless all of
// them are, so the person asking is never left wondering who was.
func (s *Service) RingInto(ctx context.Context, orgID, callID, ringerID uuid.UUID, named []uuid.UUID) ([]RungPerson, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	if call.Status != "active" {
		return nil, ErrCallNotGoing
	}
	if in, err := s.inCallNow(ctx, call, ringerID); err != nil {
		return nil, err
	} else if !in {
		return nil, ErrNotInThisCall
	}

	targets := make([]uuid.UUID, 0, len(named))
	seen := map[uuid.UUID]bool{}
	for _, id := range named {
		if id == ringerID {
			return nil, apperrors.New(apperrors.CodeBadRequest, "you are already in this call")
		}
		if !seen[id] {
			seen[id] = true
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "choose somebody to ring")
	}
	if len(targets) > maxRingAtOnce {
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf("ring at most %d people at once", maxRingAtOnce))
	}

	// Everybody named is checked before anybody is rung.
	now := s.clock().UTC().Truncate(time.Microsecond)
	for _, id := range targets {
		active, err := s.isActivePerson(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		if !active {
			// The same answer whether they never existed, are in another
			// organization or have left: a ring is a way into this room.
			return nil, apperrors.New(apperrors.CodeBadRequest, "somebody you chose is not a member of this organization")
		}
		name := s.DisplayName(ctx, id)
		in, err := s.inCallNow(ctx, call, id)
		if err != nil {
			return nil, err
		}
		if in {
			return nil, apperrors.New(apperrors.CodeConflict, name+" is already in this call")
		}
		ringing, err := s.beingRung(ctx, call, id, now)
		if err != nil {
			return nil, err
		}
		if ringing {
			return nil, apperrors.New(apperrors.CodeConflict, name+" is already being rung")
		}
		var recent int
		if err := s.db.QueryRow(ctx, `
			SELECT COUNT(*) FROM call_events WHERE call_id = $1 AND user_id = $2 AND type = 'rung' AND at > $3
		`, call.ID, id, now.Add(-ringRepeatWindow)).Scan(&recent); err != nil {
			return nil, fmt.Errorf("count recent rings: %w", err)
		}
		if recent >= ringRepeatLimit {
			return nil, apperrors.New(apperrors.CodeRateLimited, name+" has been rung several times already; try again in a few minutes")
		}
	}
	present, err := s.peopleInCallNow(ctx, call.ID)
	if err != nil {
		return nil, err
	}
	if len(present)+len(targets) > s.MaxParticipants() {
		return nil, apperrors.New(apperrors.CodeConflict, "this call cannot hold that many people")
	}

	// They are invited from now on: their token request is allowed (MayJoin),
	// and the call is on their history.
	for _, id := range targets {
		if _, err := s.db.Exec(ctx, `
			INSERT INTO call_participants (call_id, user_id, role, rung_by, rung_at)
			VALUES ($1, $2, 'participant', $3, $4)
			ON CONFLICT (call_id, user_id) DO UPDATE
			SET rung_by = EXCLUDED.rung_by, rung_at = EXCLUDED.rung_at, ring_ended_at = NULL,
			    -- Somebody who never came in is waiting to answer again. Somebody who
			    -- was in and left keeps when they left, which their sessions decide.
			    left_at = CASE WHEN call_participants.joined_at IS NULL THEN NULL ELSE call_participants.left_at END,
			    answered_at = CASE WHEN call_participants.joined_at IS NULL THEN NULL ELSE call_participants.answered_at END
		`, call.ID, id, ringerID, now); err != nil {
			return nil, fmt.Errorf("add somebody rung into a call: %w", err)
		}
		if _, err := s.db.Exec(ctx, insertEvent, call.ID, "rung", id, ringerID, now, "rung:"+uuid.NewString()); err != nil {
			return nil, fmt.Errorf("record a ring: %w", err)
		}
	}

	ringerName := s.DisplayName(ctx, ringerID)
	asCall := &Call{
		ID: call.ID, OrgID: call.OrgID, ChannelID: call.ChannelID, RoomName: call.RoomName,
		CallType: call.CallType, Status: call.Status, CreatedBy: call.CreatedBy,
	}

	result := make([]RungPerson, 0, len(targets))
	free, away := s.splitOnLeave(ctx, orgID, targets)
	free, busy := s.splitBusy(ctx, asCall, free)
	for id, back := range away {
		result = append(result, RungPerson{UserID: id, Name: s.DisplayName(ctx, id), Status: RingOnLeave, BackOn: back})
		s.endAddedRing(ctx, call, id, RingOnLeave, nil)
	}
	for _, id := range busy {
		result = append(result, RungPerson{UserID: id, Name: s.DisplayName(ctx, id), Status: RingBusy})
		s.endAddedRing(ctx, call, id, RingBusy, nil)
	}
	if len(free) == 0 {
		return result, nil
	}

	// Who else is there, for "Asha is calling you into a call with Ravi".
	with := []string{}
	for _, id := range present {
		if id != ringerID {
			if name := s.DisplayName(ctx, id); name != "" {
				with = append(with, name)
			}
		}
	}
	s.ringDevices(ctx, asCall, ringerName, intoCallBody(call.CallType, with), free)

	inCall := append(present, ringerID)
	for _, id := range free {
		name := s.DisplayName(ctx, id)
		result = append(result, RungPerson{UserID: id, Name: name, Status: "ringing"})
		if s.broadcaster == nil {
			continue
		}
		token, err := s.GenerateToken(ctx, call.RoomName, id.String(), name, true, true)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not mint a token for somebody rung into a call")
		} else {
			payload := protocol.CallInvitedPayload{
				CallID: call.ID.String(), RoomName: call.RoomName,
				CallerID: ringerID.String(), CallerName: ringerName,
				CallType: call.CallType, Token: token,
				Ongoing: true, With: with,
			}
			if call.ChannelID != nil {
				payload.ChannelID = call.ChannelID.String()
			}
			s.broadcaster.BroadcastToUser(id.String(), protocol.NewMessage(protocol.TypeCallInvited, payload))
		}
		frame := protocol.NewMessage(protocol.TypeCallAdding, map[string]any{
			"callId": call.ID.String(), "userId": id.String(), "name": name,
			"by": ringerName, "reachable": s.isReachable(id.String()),
		})
		for _, to := range distinct(inCall) {
			s.broadcaster.BroadcastToUser(to.String(), frame)
		}
	}

	// A phone left ringing in an empty office rings out, and the people in the
	// call hear so, whether or not a tab was open to say it.
	select {
	case <-s.ringStop:
		return result, nil
	default:
	}
	// Read now: the timer outlives this request, and the setting may change.
	ringFor := s.ringFor
	s.ringers.Add(1)
	go func() {
		defer s.ringers.Done()
		select {
		case <-s.ringStop:
			return
		case <-time.After(ringFor):
		}
		ctx := context.WithoutCancel(ctx)
		for _, id := range free {
			s.endAddedRing(ctx, call, id, RingNoAnswer, &now)
		}
	}()
	return result, nil
}

// CancelRing stops ringing somebody who was rung into a call, on behalf of
// somebody in it.
func (s *Service) CancelRing(ctx context.Context, orgID, callID, byID, userID uuid.UUID) error {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return err
	}
	if in, err := s.inCallNow(ctx, call, byID); err != nil {
		return err
	} else if !in {
		return ErrNotInThisCall
	}
	if !s.endAddedRing(ctx, call, userID, RingCancelled, nil) {
		return ErrNotRinging
	}
	return nil
}

// endAddedRing stops a ring into a call, once: whichever of the person
// answering, turning it down, it ringing out or somebody cancelling it comes
// first is how it ended, and the rest find nothing to do. ringAt, when given,
// stops only that ring, so a ring running out does not stop a later one. It
// says whether this is what stopped it.
//
// Somebody who never came in is no longer waiting to answer. Nothing about the
// call itself changes: it goes on for whoever is in it.
func (s *Service) endAddedRing(ctx context.Context, call *CallModel, userID uuid.UUID, outcome string, ringAt *time.Time) bool {
	now := s.clock()
	// Somebody who has picked up since this ring began, or is connected now, has
	// answered it, whatever else arrives a moment later: they are not marked as
	// gone, and the call is not told they did not answer. Decided in the same
	// statement that ends the ring, so nothing can come in between.
	tag, err := s.db.Exec(ctx, `
		UPDATE call_participants p
		SET ring_ended_at = $3,
		    left_at = CASE WHEN p.joined_at IS NULL THEN COALESCE(p.left_at, $3) ELSE p.left_at END
		WHERE p.call_id = $1 AND p.user_id = $2 AND p.rung_at IS NOT NULL AND p.ring_ended_at IS NULL
		  AND ($4::timestamptz IS NULL OR p.rung_at = $4)
		  AND ($5 OR NOT (
		      (p.answered_at IS NOT NULL AND p.answered_at >= p.rung_at)
		      OR EXISTS (SELECT 1 FROM call_sessions cs
		                 WHERE cs.call_id = p.call_id AND cs.user_id = p.user_id
		                   AND cs.joined_at IS NOT NULL AND cs.left_at IS NULL)))
	`, call.ID, userID, now, ringAt, outcome == RingAnswered)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not stop a ring into a call")
		return false
	}
	if tag.RowsAffected() == 0 {
		return false
	}
	// Somebody who picked up is on their way in, and is not told the call ended.
	if outcome != RingBusy && outcome != RingOnLeave && outcome != RingAnswered {
		// Their phone, and every tab they have open, stops ringing.
		s.stopRinging(ctx, call.ID, []uuid.UUID{userID})
		if s.broadcaster != nil {
			s.broadcaster.BroadcastToUser(userID.String(), protocol.NewMessage(protocol.TypeCallEnded, protocol.CallEndedPayload{
				CallID: call.ID.String(), RoomName: call.RoomName, Reason: RingStoppedPrefix + outcome,
			}))
		}
	}
	s.tellRingResult(ctx, call, userID, outcome)
	return true
}

// tellRingResult tells everybody in the call, and whoever rang, how a ring
// into it ended.
func (s *Service) tellRingResult(ctx context.Context, call *CallModel, userID uuid.UUID, outcome string) {
	if s.broadcaster == nil || call.Status != "active" {
		return
	}
	var rungBy *uuid.UUID
	if err := s.db.QueryRow(ctx,
		`SELECT rung_by FROM call_participants WHERE call_id = $1 AND user_id = $2`,
		call.ID, userID).Scan(&rungBy); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.logger.Warn().Err(err).Msg("could not read who rang somebody into a call")
	}
	to, err := s.peopleInCallNow(ctx, call.ID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not tell who is in a call")
	}
	if rungBy != nil {
		to = append(to, *rungBy)
	}
	frame := protocol.NewMessage(protocol.TypeCallRingResult, map[string]any{
		"callId": call.ID.String(), "userId": userID.String(),
		"name": s.DisplayName(ctx, userID), "outcome": outcome,
	})
	for _, id := range distinct(to) {
		if id != userID {
			s.broadcaster.BroadcastToUser(id.String(), frame)
		}
	}
}

// wasRungInto says whether somebody's part in a call is a ring into it, which
// one person turning down never ends.
func (s *Service) wasRungInto(ctx context.Context, callID, userID uuid.UUID) (bool, error) {
	var rung bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM call_participants WHERE call_id = $1 AND user_id = $2 AND rung_at IS NOT NULL)
	`, callID, userID).Scan(&rung)
	if err != nil {
		return false, fmt.Errorf("check ring into call: %w", err)
	}
	return rung, nil
}

// answeredAddedRing stops a ring into a call because they picked up, and says so
// to the people in it.
func (s *Service) answeredAddedRing(ctx context.Context, call *CallModel, userID uuid.UUID) {
	s.endAddedRing(ctx, call, userID, RingAnswered, nil)
}

// beingRung says whether somebody is being rung for this call now: by a ring
// into it, or still by the ring the call started with.
func (s *Service) beingRung(ctx context.Context, call *CallModel, userID uuid.UUID, now time.Time) (bool, error) {
	var ringing bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM call_participants p JOIN calls c ON c.id = p.call_id
			WHERE p.call_id = $1 AND p.user_id = $2 AND (
			    (p.rung_at IS NOT NULL AND p.ring_ended_at IS NULL AND p.rung_at > $3)
			 OR (p.rung_at IS NULL AND p.user_id <> c.created_by AND c.scheduled_start IS NULL
			     AND p.joined_at IS NULL AND p.answered_at IS NULL AND p.left_at IS NULL
			     AND c.started_at > $3)))
	`, call.ID, userID, now.Add(-RingWindow)).Scan(&ringing)
	if err != nil {
		return false, fmt.Errorf("check whether somebody is being rung: %w", err)
	}
	return ringing, nil
}

// inCallNow says whether somebody is connected to a call right now. The media
// server knows; without one, the attendance record and who picked up answer.
func (s *Service) inCallNow(ctx context.Context, call *CallModel, userID uuid.UUID) (bool, error) {
	in, err := s.mediaOf().InRoom(ctx, call.RoomName, userID.String())
	if err == nil {
		return in, nil
	}
	if !errors.Is(err, errNoMedia) {
		return false, fmt.Errorf("ask the media server who is in the call: %w", err)
	}
	present, err := s.peopleInCallNow(ctx, call.ID)
	if err != nil {
		return false, err
	}
	for _, id := range present {
		if id == userID {
			return true, nil
		}
	}
	return false, nil
}

// peopleInCallNow is who is in a call: connected, by the attendance record, or
// having picked up and not left when no connection was reported at all.
func (s *Service) peopleInCallNow(ctx context.Context, callID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT user_id FROM call_sessions
		WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL
		UNION
		SELECT p.user_id FROM call_participants p
		WHERE p.call_id = $1 AND p.answered_at IS NOT NULL AND p.left_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM call_sessions cs WHERE cs.call_id = p.call_id AND cs.user_id = p.user_id)
	`, callID)
	if err != nil {
		return nil, fmt.Errorf("list who is in a call: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("list who is in a call: %w", err)
	}
	return ids, nil
}

// intoCallBody is what a phone's notification says under the caller's name.
func intoCallBody(callType string, with []string) string {
	kind := "video call"
	if callType == "audio" {
		kind = "call"
	}
	if len(with) == 0 {
		return "Calling you into a " + kind
	}
	return "Calling you into a " + kind + " with " + namesList(with)
}

// namesList is "Ravi", "Ravi and Sam", "Ravi, Sam and 2 others".
func namesList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	case 3:
		return names[0] + ", " + names[1] + " and " + names[2]
	}
	return strings.Join(names[:2], ", ") + fmt.Sprintf(" and %d others", len(names)-2)
}

func distinct(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// othersInCall names who is in a call apart from the caller and the person
// being rung, for a ring delivered late.
func (s *Service) othersInCall(ctx context.Context, callID, callerID, userID uuid.UUID) []string {
	present, err := s.peopleInCallNow(ctx, callID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not tell who is in a call")
		return nil
	}
	names := []string{}
	for _, id := range present {
		if id != callerID && id != userID {
			if name := s.DisplayName(ctx, id); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// RingInto is POST /calls/:id/ring: somebody in a call rings colleagues into it.
func (h *Handler) RingInto(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req struct {
		UserIDs []string `json:"userIds" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name the people to ring"})
		return
	}
	named := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, raw := range req.UserIDs {
		target, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userIds"})
			return
		}
		named = append(named, target)
	}
	// Not cut short by the person asking going away: the ring is already out.
	rung, err := h.service.RingInto(context.WithoutCancel(c.Request.Context()), orgID, id, userID, named)
	if err != nil {
		httpx.Error(c, err, "failed to ring")
		return
	}
	c.JSON(http.StatusOK, gin.H{"rung": rung})
}

// CancelRing is DELETE /calls/:id/ring/:userId: stop ringing somebody.
func (h *Handler) CancelRing(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	target, ok := pathID(c, "userId")
	if !ok {
		return
	}
	if err := h.service.CancelRing(c.Request.Context(), orgID, id, userID, target); err != nil {
		httpx.Error(c, err, "failed to stop ringing")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "stopped ringing"})
}

// EndExpiredRings ends every ring into a call that has rung for longer than
// RingWindow, as "did not answer", and returns how many. The timer started with
// a ring does this while the server stays up; a restart or a deploy loses that
// timer, and this is what ends those rings instead. Every replica runs it, and
// a ring ends once whichever gets there first.
func (s *Service) EndExpiredRings(ctx context.Context) (int, error) {
	type expired struct {
		orgID, callID, userID uuid.UUID
		rungAt                time.Time
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.org_id, p.call_id, p.user_id, p.rung_at
		FROM call_participants p JOIN calls c ON c.id = p.call_id
		WHERE p.rung_at IS NOT NULL AND p.ring_ended_at IS NULL AND p.rung_at <= $1
	`, s.clock().Add(-RingWindow))
	if err != nil {
		return 0, fmt.Errorf("find rings that have run out: %w", err)
	}
	found, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (expired, error) {
		var e expired
		err := row.Scan(&e.orgID, &e.callID, &e.userID, &e.rungAt)
		return e, err
	})
	if err != nil {
		return 0, fmt.Errorf("find rings that have run out: %w", err)
	}
	ended := 0
	for _, e := range found {
		call, err := s.GetCall(ctx, e.orgID, e.callID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not read a call to end a ring into it")
			continue
		}
		rungAt := e.rungAt
		if s.endAddedRing(ctx, call, e.userID, RingNoAnswer, &rungAt) {
			ended++
		}
	}
	return ended, nil
}

// RunExpiredRingSweep ends rings that have run out until ctx ends.
func (s *Service) RunExpiredRingSweep(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.EndExpiredRings(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn().Err(err).Msg("could not end rings that have run out")
			}
		}
	}
}
