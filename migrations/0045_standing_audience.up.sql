-- Who a standing meeting is for: everybody in the organization, as every standing
-- meeting was until now, or chosen teams (with the teams nested beneath them) and
-- chosen people. Membership is worked out when somebody asks, so a person who
-- joins a team can join its stand-up at once and one who leaves cannot.
ALTER TABLE standing_meetings ADD COLUMN IF NOT EXISTS audience VARCHAR(16) NOT NULL DEFAULT 'everyone'
    CHECK (audience IN ('everyone', 'chosen'));

CREATE TABLE IF NOT EXISTS standing_meeting_teams (
    standing_meeting_id UUID NOT NULL REFERENCES standing_meetings(id) ON DELETE CASCADE,
    team_id             UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    PRIMARY KEY (standing_meeting_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_standing_meeting_teams_team ON standing_meeting_teams (team_id);

CREATE TABLE IF NOT EXISTS standing_meeting_people (
    standing_meeting_id UUID NOT NULL REFERENCES standing_meetings(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (standing_meeting_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_standing_meeting_people_user ON standing_meeting_people (user_id);

-- The one statement of who is in a standing meeting's audience, read by joining,
-- the list of meetings, the calendar, attendance and answering a knock alike. The
-- person has to be active in the meeting's organization; the meeting's creator is
-- always in it, because a manager is on no team of their own.
CREATE OR REPLACE FUNCTION standing_audience_includes(meeting UUID, person UUID) RETURNS BOOLEAN
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM standing_meetings sm
        JOIN users u ON u.id = person AND u.org_id = sm.org_id
                    AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
        WHERE sm.id = meeting AND (
            sm.audience = 'everyone'
            OR sm.created_by = person
            OR EXISTS (SELECT 1 FROM standing_meeting_people p
                       WHERE p.standing_meeting_id = sm.id AND p.user_id = person)
            OR EXISTS (
                WITH RECURSIVE reach AS (
                    SELECT t.id FROM standing_meeting_teams smt
                    JOIN teams t ON t.id = smt.team_id AND t.deleted_at IS NULL
                    WHERE smt.standing_meeting_id = sm.id
                    UNION
                    SELECT child.id FROM teams child
                    JOIN reach ON child.parent_id = reach.id
                    WHERE child.deleted_at IS NULL
                )
                SELECT 1 FROM team_members tm JOIN reach ON reach.id = tm.team_id
                WHERE tm.user_id = person)))
$$;
