-- A task that moves into a project takes the project's key (TASK-7 becomes
-- PHO-31). What was already made under the old key, a branch named task-7-...,
-- a commit that says TASK-7, still finds the task through the keys it had.
CREATE TABLE IF NOT EXISTS task_key_aliases (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    task_key   VARCHAR(32) NOT NULL,
    task_id    UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_key_aliases_key ON task_key_aliases (org_id, UPPER(task_key));
CREATE INDEX IF NOT EXISTS idx_task_key_aliases_task ON task_key_aliases (task_id);

-- A branch started for a task from TeamUp, on one of its project's repositories.
CREATE TABLE IF NOT EXISTS task_branches (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    task_id       UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    repository_id UUID NOT NULL REFERENCES code_repositories(id) ON DELETE CASCADE,
    name          VARCHAR(255) NOT NULL,
    base          VARCHAR(255) NOT NULL,
    html_url      TEXT NOT NULL DEFAULT '',
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, name)
);
CREATE INDEX IF NOT EXISTS idx_task_branches_task ON task_branches (task_id, created_at);
