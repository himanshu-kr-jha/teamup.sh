package calls

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"

	"github.com/teamup/teamup/pkg/protocol"
)

// Somebody already in a call or a meeting is not rung for another. Whoever is
// calling is told straight away that they are busy, rather than listening to a
// ring nobody will answer, and the person called finds a note in their chat
// that they were called while they were busy.

// inAnotherCall reports whether somebody is connected to a call other than
// this one. The attendance record (call_sessions, from LiveKit's webhooks)
// names the rooms they may be in; having picked up a ring and not put it down
// (call_participants) stands in for it when the webhooks are not arriving.
// The media server, when there is one, has the last word: somebody who has
// just closed their tab is still in the record for the moment it takes the
// webhook to arrive, and is not busy.
func (s *Service) inAnotherCall(ctx context.Context, orgID, userID, callID uuid.UUID) (bool, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.room_name FROM calls c
		WHERE c.org_id = $1 AND c.id <> $3 AND c.status = 'active'
		  AND (EXISTS (SELECT 1 FROM call_sessions cs
		               WHERE cs.call_id = c.id AND cs.user_id = $2
		                 AND cs.joined_at IS NOT NULL AND cs.left_at IS NULL)
		    OR EXISTS (SELECT 1 FROM call_participants p
		               WHERE p.call_id = c.id AND p.user_id = $2
		                 AND p.answered_at IS NOT NULL AND p.left_at IS NULL
		                 AND NOT EXISTS (SELECT 1 FROM call_sessions cs2
		                                 WHERE cs2.call_id = c.id AND cs2.user_id = $2)))
	`, orgID, userID, callID)
	if err != nil {
		return false, fmt.Errorf("check whether somebody is in a call: %w", err)
	}
	var rooms []string
	for rows.Next() {
		var room string
		if err := rows.Scan(&room); err != nil {
			rows.Close()
			return false, fmt.Errorf("check whether somebody is in a call: %w", err)
		}
		rooms = append(rooms, room)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(rooms) == 0 {
		return false, nil
	}
	if s.roomService == nil {
		return true, nil
	}
	for _, room := range rooms {
		res, err := s.roomService.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: room})
		if err != nil {
			return false, fmt.Errorf("ask the media server who is in a call: %w", err)
		}
		for _, p := range res.GetParticipants() {
			if p.GetIdentity() == userID.String() && p.GetState() != livekit.ParticipantInfo_DISCONNECTED {
				return true, nil
			}
		}
	}
	return false, nil
}

// splitBusy separates the people who can be rung from the ones already in
// another call. Anybody whose state cannot be read is rung: a ring too many is
// a smaller harm than a call that never arrives.
func (s *Service) splitBusy(ctx context.Context, call *Call, invitees []uuid.UUID) (free, busy []uuid.UUID) {
	for _, id := range invitees {
		inCall, err := s.inAnotherCall(ctx, call.OrgID, id, call.ID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not tell whether somebody is in a call; ringing them")
		}
		if inCall {
			busy = append(busy, id)
		} else {
			free = append(free, id)
		}
	}
	return free, busy
}

// answerBusy tells whoever is calling that these people are in another call,
// and settles their part in this one the way a decline would.
func (s *Service) answerBusy(ctx context.Context, call *Call, callerID string, busy []uuid.UUID) {
	if s.broadcaster != nil {
		for _, id := range busy {
			s.broadcaster.BroadcastToUser(callerID, protocol.NewMessage("call.reach", map[string]any{
				"callId":    call.ID.String(),
				"userId":    id.String(),
				"reachable": false,
				"busy":      true,
			}))
		}
	}
	model, err := s.GetCall(ctx, call.OrgID, call.ID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not read a call to say who is busy")
		return
	}
	for _, id := range busy {
		if err := s.TurnedDown(ctx, call.OrgID, model, id, "busy"); err != nil {
			s.logger.Warn().Err(err).Msg("could not settle a busy person's part in a call")
		}
	}
}

// TurnedDown records that somebody who was called will not be picking up, and
// why: "declined", "no_answer", or "busy". Whoever called is told, and a call
// nobody is left to answer and nobody joined is ended, which leaves the note
// in the conversation between the two of them.
func (s *Service) TurnedDown(ctx context.Context, orgID uuid.UUID, call *CallModel, userID uuid.UUID, reason string) error {
	if err := s.MarkParticipantLeft(ctx, call.ID, userID); err != nil {
		return err
	}
	// Whoever started it, not everybody: one person not coming does not end
	// the call for the rest.
	s.AnnounceCallOver(ctx, call, reason, s.DisplayName(ctx, userID), []string{call.CreatedBy.String()})

	waiting, err := s.AnybodyLeftToAnswer(ctx, call.ID, call.CreatedBy)
	if err != nil || waiting {
		return err
	}
	joined, err := s.SomebodyJoined(ctx, call.ID, call.CreatedBy)
	if err != nil || joined {
		return err
	}
	ended, err := s.endRecordThenRoom(ctx, orgID, call)
	if err != nil {
		return err
	}
	if ended {
		s.NoteIfMissed(ctx, call.ID, reason)
	}
	return nil
}

// NoteTakerIdentityPrefix starts the note-taker's identity in a room, as
// notetaker.IdentityPrefix does. The note-taker is not a person in the meeting.
const NoteTakerIdentityPrefix = "notes-"

// tellJoined tells the join hook (the note-taker) that a call has become a
// meeting: two or more people in it together. One person waiting for somebody
// to pick up, however long for, is not a meeting, and gets no minutes.
func (s *Service) tellJoined(ctx context.Context, call *webhookCall, roomName string) {
	if s.joined == nil || call.Status == "ended" {
		return
	}
	n, err := s.peopleInRoom(ctx, call.ID, roomName)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not count who is in a call")
		return
	}
	if n < 2 {
		return
	}
	s.joined(context.WithoutCancel(ctx), call.OrgID, call.ID, roomName)
}

// peopleInRoom counts the people connected to a call now, guests included and
// the note-taker not. The media server is asked when there is one, because it
// alone knows about guests; without one, the attendance record answers.
func (s *Service) peopleInRoom(ctx context.Context, callID uuid.UUID, roomName string) (int, error) {
	if s.roomService != nil {
		res, err := s.roomService.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
		if err != nil {
			return 0, fmt.Errorf("list call participants: %w", err)
		}
		seen := map[string]bool{}
		for _, p := range res.GetParticipants() {
			if id := p.GetIdentity(); id != "" && !strings.HasPrefix(id, NoteTakerIdentityPrefix) {
				seen[id] = true
			}
		}
		return len(seen), nil
	}
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT user_id) FROM call_sessions
		WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL
	`, callID).Scan(&n)
	return n, err
}
