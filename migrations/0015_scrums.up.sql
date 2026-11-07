-- A completed scrum is a record of what was true when it was held, so names,
-- titles and statuses are copied in rather than joined at read time. Renaming
-- somebody or editing a task later must not rewrite history.
--
-- For the same reason user_id and task_id on the two child tables are plain
-- values and not foreign keys: a snapshot outlives what it describes.
CREATE TABLE IF NOT EXISTS scrums (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- NULL means everybody the holder answered for at that moment; scope says
    -- whether that was their teams or the whole organization.
    team_id      UUID REFERENCES teams(id) ON DELETE SET NULL,
    team_name    VARCHAR(255),
    scope        VARCHAR(10) NOT NULL CHECK (scope IN ('team', 'org')),
    held_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    held_by_name VARCHAR(255) NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    -- The start of the window its "done" entries cover.
    done_since   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS scrum_participants (
    scrum_id  UUID NOT NULL REFERENCES scrums(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL,
    name      VARCHAR(255) NOT NULL,
    pending   INTEGER NOT NULL,
    ongoing   INTEGER NOT NULL,
    blocked   INTEGER NOT NULL,
    overdue   INTEGER NOT NULL,
    completed INTEGER NOT NULL,
    PRIMARY KEY (scrum_id, user_id)
);

CREATE TABLE IF NOT EXISTS scrum_entries (
    scrum_id     UUID NOT NULL REFERENCES scrums(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    task_id      UUID NOT NULL,
    title        VARCHAR(500) NOT NULL,
    status       VARCHAR(30) NOT NULL,
    priority     VARCHAR(20) NOT NULL,
    overdue      BOOLEAN NOT NULL,
    due_date     TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (scrum_id, user_id, task_id)
);

-- History is read newest first, whole organization, by team, or by holder.
CREATE INDEX IF NOT EXISTS idx_scrums_org_created ON scrums (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_scrums_team_created ON scrums (team_id, created_at DESC) WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_scrums_holder_created ON scrums (held_by, created_at DESC) WHERE held_by IS NOT NULL;

-- Contribution numbers: completions by person and date, and the two ways of
-- being on a task that had no lookup by person.
CREATE INDEX IF NOT EXISTS idx_tasks_assignee_completed
    ON tasks (assignee_id, completed_at DESC)
    WHERE completed_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_task_watchers_user ON task_watchers (user_id);
CREATE INDEX IF NOT EXISTS idx_task_comments_user ON task_comments (user_id) WHERE deleted_at IS NULL;

-- Tracking counts work by completed_at, so tasks finished before it was set reliably would never count.
UPDATE tasks SET completed_at = updated_at WHERE status = 'done' AND completed_at IS NULL;
