-- Who has access to a project's repositories, who asked for it, and every
-- step along the way: access to the company's code is given and taken away
-- here, so that there is one record of it.

CREATE TABLE IF NOT EXISTS project_access_requests (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    requester_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level        VARCHAR(10) NOT NULL CHECK (level IN ('read', 'write')),
    reason       TEXT NOT NULL DEFAULT '',
    status       VARCHAR(12) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'declined', 'cancelled')),
    decided_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_at   TIMESTAMPTZ,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- One open request per person and project.
CREATE UNIQUE INDEX IF NOT EXISTS idx_access_requests_pending ON project_access_requests (project_id, requester_id) WHERE status = 'pending';

-- Access somebody holds to a project, until it is revoked.
CREATE TABLE IF NOT EXISTS project_access_grants (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level       VARCHAR(10) NOT NULL CHECK (level IN ('read', 'write')),
    request_id  UUID REFERENCES project_access_requests(id) ON DELETE SET NULL,
    granted_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    revoked_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_access_grants_live ON project_access_grants (project_id, user_id) WHERE revoked_at IS NULL;

-- What happened on each repository when access was given or taken away.
CREATE TABLE IF NOT EXISTS project_access_repos (
    grant_id      UUID NOT NULL REFERENCES project_access_grants(id) ON DELETE CASCADE,
    repository_id UUID NOT NULL REFERENCES code_repositories(id) ON DELETE CASCADE,
    -- granted, invited (waiting for them to accept on GitHub), failed or revoked.
    status        VARCHAR(12) NOT NULL,
    detail        TEXT NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (grant_id, repository_id)
);

-- The record: requested, approved, declined, cancelled, granted, revoked,
-- failed, and changes made on GitHub directly.
CREATE TABLE IF NOT EXISTS project_access_log (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    subject_id UUID REFERENCES users(id) ON DELETE SET NULL,
    subject    VARCHAR(255) NOT NULL DEFAULT '',
    actor_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    action     VARCHAR(30) NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_access_log_org ON project_access_log (org_id, created_at DESC);
