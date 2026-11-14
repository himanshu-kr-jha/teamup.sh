package calls

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Who a standing meeting is for (migration 0045). A meeting is for everybody in
// the organization, as every standing meeting used to be, or for chosen teams,
// with the teams nested beneath them, and chosen people. Whoever made it is
// always in it.
//
// Who is in it is decided by the database function standing_audience_includes
// when somebody asks, never copied anywhere, so joining a team brings its
// stand-up with it and leaving takes it away. Somebody outside is not shown the
// meeting at all; with a link to a day of it they meet the door like any private
// call, and being let in admits them to that day only, because each day is its
// own call.

// AudienceRef is a team or a person, by name.
type AudienceRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Audience is who a meeting is for.
type Audience struct {
	Everyone bool          `json:"everyone"`
	Teams    []AudienceRef `json:"teams"`
	People   []AudienceRef `json:"people"`
}

// Actor is somebody making or changing a standing meeting.
type Actor struct {
	ID uuid.UUID
	// Admin may choose anybody, including the whole organization, and manage
	// every meeting.
	Admin bool
}

// AudienceOptions is what an actor may choose from.
type AudienceOptions struct {
	Everyone bool          `json:"everyone"`
	Teams    []AudienceRef `json:"teams"`
	People   []AudienceRef `json:"people"`
}

// inStandingAudience is the database's answer for one person and one meeting.
func (s *Service) inStandingAudience(ctx context.Context, meetingID, userID uuid.UUID) (bool, error) {
	var in bool
	if err := s.db.QueryRow(ctx, `SELECT standing_audience_includes($1, $2)`, meetingID, userID).Scan(&in); err != nil {
		return false, fmt.Errorf("check standing meeting audience: %w", err)
	}
	return in, nil
}

// StandingAudienceOptions lists the teams and people an actor may make a standing
// meeting for: anybody for an administrator; for a manager, the teams they manage
// with those nested beneath, and the people they answer for. Somebody who manages
// nobody may make none.
func (s *Service) StandingAudienceOptions(ctx context.Context, orgID uuid.UUID, a Actor) (*AudienceOptions, error) {
	out := &AudienceOptions{Everyone: a.Admin, Teams: []AudienceRef{}, People: []AudienceRef{}}
	if a.Admin {
		teams, err := s.refs(ctx, `SELECT id, name FROM teams WHERE org_id = $1 AND deleted_at IS NULL ORDER BY name, id`, orgID)
		if err != nil {
			return nil, err
		}
		people, err := s.refs(ctx, `
			SELECT id, name FROM users WHERE org_id = $1 AND deleted_at IS NULL AND COALESCE(is_active, true)
			ORDER BY name, id`, orgID)
		if err != nil {
			return nil, err
		}
		out.Teams, out.People = teams, people
		return out, nil
	}
	if s.chart == nil {
		return out, nil
	}
	managed, err := s.chart.ManagedTeams(ctx, orgID, a.ID)
	if err != nil {
		return nil, err
	}
	if len(managed) == 0 {
		return out, nil
	}
	for _, t := range managed {
		out.Teams = append(out.Teams, AudienceRef{ID: t.ID, Name: t.Name})
	}
	_, ids, err := s.chart.AnswerableFor(ctx, orgID, a.ID, false, nil)
	if err != nil {
		return nil, err
	}
	people, err := s.refs(ctx, `
		SELECT id, name FROM users WHERE org_id = $1 AND id = ANY($2) AND id <> $3
		  AND deleted_at IS NULL AND COALESCE(is_active, true)
		ORDER BY name, id`, orgID, ids, a.ID)
	if err != nil {
		return nil, err
	}
	out.People = people
	return out, nil
}

func (s *Service) refs(ctx context.Context, query string, args ...any) ([]AudienceRef, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audience choices: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (AudienceRef, error) {
		var r AudienceRef
		err := row.Scan(&r.ID, &r.Name)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan audience choice: %w", err)
	}
	return out, nil
}

// MayMakeStanding is whether an actor may make standing meetings at all.
func (s *Service) MayMakeStanding(ctx context.Context, orgID uuid.UUID, a Actor) (bool, error) {
	if a.Admin {
		return true, nil
	}
	if s.chart == nil {
		return false, nil
	}
	managed, err := s.chart.ManagedTeams(ctx, orgID, a.ID)
	return len(managed) > 0, err
}

// audienceChoice is what was asked for, before it is checked.
type audienceChoice struct {
	everyone bool
	teams    []uuid.UUID
	people   []uuid.UUID
}

// checkAudience refuses a choice the actor may not make: the whole organization
// unless an administrator, a team or person outside what they may choose, or
// nobody at all. What the meeting already has (kept) may stay, so a manager
// editing their meeting is not refused over somebody an admin added.
func (s *Service) checkAudience(ctx context.Context, orgID uuid.UUID, a Actor, c audienceChoice, kept audienceChoice) error {
	if c.everyone {
		if !a.Admin {
			return apperrors.New(apperrors.CodeForbidden, "only an organization admin can make a standing meeting for everybody; choose your teams or people")
		}
		return nil
	}
	if len(c.teams) == 0 && len(c.people) == 0 {
		return badRequest("choose at least one team or person, or everybody")
	}
	opts, err := s.StandingAudienceOptions(ctx, orgID, a)
	if err != nil {
		return err
	}
	in := func(id uuid.UUID, list []AudienceRef) bool {
		return slices.ContainsFunc(list, func(r AudienceRef) bool { return r.ID == id })
	}
	for _, id := range c.teams {
		if !in(id, opts.Teams) && !slices.Contains(kept.teams, id) {
			return apperrors.New(apperrors.CodeForbidden, "you can only choose teams you manage")
		}
	}
	for _, id := range c.people {
		// A manager is in their own meeting anyway, as its creator.
		if id == a.ID {
			continue
		}
		if !in(id, opts.People) && !slices.Contains(kept.people, id) {
			return apperrors.New(apperrors.CodeForbidden, "you can only choose people you manage")
		}
	}
	return nil
}

// currentAudience is the teams and people a meeting has now.
func (s *Service) currentAudience(ctx context.Context, meetingID uuid.UUID) (audienceChoice, error) {
	var c audienceChoice
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE((SELECT array_agg(team_id) FROM standing_meeting_teams WHERE standing_meeting_id = $1), '{}'),
		       COALESCE((SELECT array_agg(user_id) FROM standing_meeting_people WHERE standing_meeting_id = $1), '{}')
	`, meetingID).Scan(&c.teams, &c.people)
	if err != nil {
		return c, fmt.Errorf("read audience: %w", err)
	}
	return c, nil
}

// saveAudience replaces the meeting's audience inside tx.
func saveAudience(ctx context.Context, tx pgx.Tx, meetingID uuid.UUID, c audienceChoice) error {
	kind := "chosen"
	if c.everyone {
		kind = "everyone"
		c.teams, c.people = nil, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE standing_meetings SET audience = $2 WHERE id = $1`, meetingID, kind); err != nil {
		return fmt.Errorf("save audience: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM standing_meeting_teams WHERE standing_meeting_id = $1`, meetingID); err != nil {
		return fmt.Errorf("clear audience teams: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM standing_meeting_people WHERE standing_meeting_id = $1`, meetingID); err != nil {
		return fmt.Errorf("clear audience people: %w", err)
	}
	if len(c.teams) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO standing_meeting_teams (standing_meeting_id, team_id)
			SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, meetingID, c.teams); err != nil {
			return fmt.Errorf("save audience teams: %w", err)
		}
	}
	if len(c.people) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO standing_meeting_people (standing_meeting_id, user_id)
			SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, meetingID, c.people); err != nil {
			return fmt.Errorf("save audience people: %w", err)
		}
	}
	return nil
}

// fillAudiences names each meeting's audience, and says for one viewer whether
// they are in it and may change it, in three queries however many meetings.
func (s *Service) fillAudiences(ctx context.Context, meetings []StandingMeeting, viewer Actor) error {
	if len(meetings) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(meetings))
	at := make(map[uuid.UUID]int, len(meetings))
	for i := range meetings {
		ids[i] = meetings[i].ID
		at[meetings[i].ID] = i
		meetings[i].Audience = &Audience{Everyone: meetings[i].audienceKind == "everyone", Teams: []AudienceRef{}, People: []AudienceRef{}}
		meetings[i].CanManage = viewer.Admin || (meetings[i].CreatedBy != nil && *meetings[i].CreatedBy == viewer.ID)
	}
	add := func(query string, put func(m *StandingMeeting, r AudienceRef)) error {
		rows, err := s.db.Query(ctx, query, ids)
		if err != nil {
			return fmt.Errorf("load audiences: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var meetingID uuid.UUID
			var r AudienceRef
			if err := rows.Scan(&meetingID, &r.ID, &r.Name); err != nil {
				return fmt.Errorf("scan audience: %w", err)
			}
			put(&meetings[at[meetingID]], r)
		}
		return rows.Err()
	}
	if err := add(`
		SELECT smt.standing_meeting_id, t.id, t.name FROM standing_meeting_teams smt
		JOIN teams t ON t.id = smt.team_id AND t.deleted_at IS NULL
		WHERE smt.standing_meeting_id = ANY($1) ORDER BY t.name, t.id`,
		func(m *StandingMeeting, r AudienceRef) { m.Audience.Teams = append(m.Audience.Teams, r) }); err != nil {
		return err
	}
	if err := add(`
		SELECT p.standing_meeting_id, u.id, u.name FROM standing_meeting_people p
		JOIN users u ON u.id = p.user_id AND u.deleted_at IS NULL
		WHERE p.standing_meeting_id = ANY($1) ORDER BY u.name, u.id`,
		func(m *StandingMeeting, r AudienceRef) { m.Audience.People = append(m.Audience.People, r) }); err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, standing_audience_includes(id, $2) FROM unnest($1::uuid[]) AS id`, ids, viewer.ID)
	if err != nil {
		return fmt.Errorf("check audiences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var in bool
		if err := rows.Scan(&id, &in); err != nil {
			return fmt.Errorf("scan audience check: %w", err)
		}
		meetings[at[id]].InAudience = in
	}
	return rows.Err()
}

// mayManageStanding is an administrator, or whoever made the meeting.
func mayManageStanding(m *StandingMeeting, a Actor) bool {
	return a.Admin || (m.CreatedBy != nil && *m.CreatedBy == a.ID)
}
