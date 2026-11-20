-- The scrum is a table, one row per person and their tasks across it, and each
-- viewer arranges it by hand. The arrangement is theirs alone: nobody else reads it.
--
-- People are ordered per view: team_id is the team filter the viewer had on, and
-- NULL is everybody they answer for. Ids are plain values, not foreign keys, so a
-- person who leaves simply stops matching and drops out when the table is drawn.
CREATE TABLE IF NOT EXISTS scrum_people_orders (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    viewer_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id    UUID REFERENCES teams(id) ON DELETE CASCADE,
    people     UUID[] NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE NULLS NOT DISTINCT (viewer_id, team_id)
);

-- Which of a person's tasks is Task 1, 2, 3, as one viewer arranged them. The same
-- in every view, because a person's work does not change with the filter.
CREATE TABLE IF NOT EXISTS scrum_task_orders (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    viewer_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    person_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tasks      UUID[] NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (viewer_id, person_id)
);

-- A saved scrum keeps the order its holder had and each person's leads. Scrums
-- saved before this have neither, and read as NULL rather than being made up.
ALTER TABLE scrum_participants ADD COLUMN IF NOT EXISTS position INTEGER;
ALTER TABLE scrum_participants ADD COLUMN IF NOT EXISTS leads TEXT[];
ALTER TABLE scrum_entries ADD COLUMN IF NOT EXISTS position INTEGER;
