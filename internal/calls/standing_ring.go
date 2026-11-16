package calls

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/leave"
	"github.com/teamup/teamup/pkg/protocol"
)

// A stand-up rings everybody it is for as it starts, the way a call does: in
// the browser and on their phone. Nobody on leave is rung, nor anybody on a
// holiday, nor somebody already in the room or talking in another call.

// standingRingLate is how long after the start a ring still goes out, so a
// server restarting across the start still rings, and one down for longer does
// not ring people into a meeting that is half over.
const standingRingLate = 5 * time.Minute

// SweepStandingRings rings every stand-up whose start has just come, once each
// day, and returns how many people were rung. The claim in standing_rings is
// what keeps several replicas from ringing twice.
func (s *Service) SweepStandingRings(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.db.Query(ctx, `SELECT org_id, `+standingColumns+` FROM standing_meetings WHERE archived_at IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("list standing meetings: %w", err)
	}
	type due struct {
		orgID uuid.UUID
		m     *StandingMeeting
	}
	var meetings []due
	for rows.Next() {
		var orgID uuid.UUID
		var m StandingMeeting
		if err := rows.Scan(&orgID, &m.ID, &m.Name, &m.TimeOfDay, &m.TimeZone, &m.Weekdays, &m.DurationMinutes,
			&m.CreatedBy, &m.ArchivedAt, &m.CreatedAt, &m.audienceKind, &m.CameraRequired, &m.AttendBeforeMinutes, &m.AttendAfterMinutes); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan standing meeting: %w", err)
		}
		meetings = append(meetings, due{orgID, &m})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	rung := 0
	for _, d := range meetings {
		n, err := s.ringStanding(ctx, d.orgID, d.m, now)
		if err != nil {
			s.logger.Warn().Err(err).Str("meeting", d.m.ID.String()).Msg("could not ring a stand-up")
			continue
		}
		rung += n
	}
	return rung, nil
}

func (s *Service) ringStanding(ctx context.Context, orgID uuid.UUID, m *StandingMeeting, now time.Time) (int, error) {
	day, err := m.occurrenceDay(now)
	if err != nil {
		return 0, err
	}
	if !m.runsOn(day.Weekday()) {
		return 0, nil
	}
	start, end, err := m.slotOn(day)
	if err != nil {
		return 0, err
	}
	// Only in the minutes after the start, and not for a slot from before the
	// meeting was made.
	if now.Before(start) || !now.Before(start.Add(standingRingLate)) || start.Before(m.CreatedAt) {
		return 0, nil
	}
	date := day.Format("2006-01-02")
	book, err := leave.Load(ctx, s.db, orgID, nil, date, date)
	if err != nil {
		return 0, err
	}
	if _, off := book.Holiday(date); off {
		return 0, nil
	}

	var claimed bool
	err = s.db.QueryRow(ctx, `
		INSERT INTO standing_rings (standing_meeting_id, occurrence_date) VALUES ($1, $2::date)
		ON CONFLICT DO NOTHING RETURNING true
	`, m.ID, date).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("claim the ring: %w", err)
	}

	creator := uuid.Nil
	if m.CreatedBy != nil {
		creator = *m.CreatedBy
	}
	if creator == uuid.Nil {
		if err := s.db.QueryRow(ctx, `SELECT id FROM users WHERE org_id = $1 AND deleted_at IS NULL ORDER BY created_at LIMIT 1`, orgID).Scan(&creator); err != nil {
			return 0, fmt.Errorf("find somebody to own the day: %w", err)
		}
	}
	callID, room, err := s.openOccurrence(ctx, orgID, m, creator, start, end, date, now, false)
	if err != nil {
		return 0, err
	}

	people, err := s.toRingForStanding(ctx, orgID, m, callID, date, start)
	if err != nil {
		return 0, err
	}
	if len(people) == 0 {
		return 0, nil
	}
	call := &Call{ID: callID, OrgID: orgID, RoomName: room, CallType: "video"}
	s.ringDevices(ctx, call, m.Name, "Starting now. Tap to join.", people)
	if s.broadcaster != nil {
		for _, id := range people {
			s.broadcaster.BroadcastToUser(id.String(), protocol.NewMessage(protocol.TypeCallInvited, protocol.CallInvitedPayload{
				CallID:     callID.String(),
				RoomName:   room,
				CallerName: m.Name,
				CallType:   "video",
				Kind:       "standing",
			}))
		}
	}
	return len(people), nil
}

// toRingForStanding is who the stand-up is for that day, less anybody on leave
// for its slot, already in its room, or in another call.
func (s *Service) toRingForStanding(ctx context.Context, orgID uuid.UUID, m *StandingMeeting, callID uuid.UUID, date string, start time.Time) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id FROM users u
		WHERE u.org_id = $1 AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
		  AND ($3 OR standing_audience_includes($2, u.id))
		  AND NOT EXISTS (SELECT 1 FROM call_sessions cs
		                  WHERE cs.call_id = $4 AND cs.user_id = u.id AND cs.left_at IS NULL)
	`, orgID, m.ID, m.audienceKind == "everyone", callID)
	if err != nil {
		return nil, fmt.Errorf("list who a stand-up is for: %w", err)
	}
	audience, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("list who a stand-up is for: %w", err)
	}
	book, err := leave.Load(ctx, s.db, orgID, audience, date, date)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(m.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("load zone: %w", err)
	}
	out := make([]uuid.UUID, 0, len(audience))
	for _, id := range audience {
		if _, away := book.On(id, date, start.In(loc)); away {
			continue
		}
		busy, err := s.inAnotherCall(ctx, orgID, id, callID)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not tell whether somebody is in a call; ringing them for the stand-up")
		}
		if busy {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// RunStandingRings sweeps until ctx ends. Every replica runs it; the claim is
// what keeps them from repeating each other.
func (s *Service) RunStandingRings(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.SweepStandingRings(ctx, s.clock()); err != nil && ctx.Err() == nil {
				s.logger.Warn().Err(err).Msg("could not ring the stand-ups")
			}
		}
	}
}
