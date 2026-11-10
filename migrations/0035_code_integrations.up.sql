-- GitHub and Bitbucket, connected by an organization's admin, and the work done
-- in them: which repositories make up which project, who is who there, and the
-- pull requests and commits that name a task.

-- One connection of an organization to a provider: a GitHub App made for it,
-- installed on its GitHub organization, or a Bitbucket workspace. What it needs
-- to act (app keys, client secrets, tokens) is sealed under TEAMUP_AUTH_SECRET.
CREATE TABLE IF NOT EXISTS code_connections (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider      VARCHAR(20) NOT NULL CHECK (provider IN ('github', 'bitbucket')),
    -- The GitHub organization's login, or the Bitbucket workspace's slug.
    account       VARCHAR(255) NOT NULL DEFAULT '',
    -- The app's numeric id on GitHub, so its webhooks can be told apart.
    app_id        VARCHAR(64) NOT NULL DEFAULT '',
    -- The GitHub App installation, once it is installed.
    installation  VARCHAR(64) NOT NULL DEFAULT '',
    -- Where the provider sends events, unguessable: /api/integrations/<provider>/<hook_key>.
    hook_key      VARCHAR(64) NOT NULL UNIQUE,
    secrets       BYTEA NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'broken')),
    last_error    TEXT NOT NULL DEFAULT '',
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, provider)
);

-- Somebody's own account on a provider, proved by signing in there once.
-- Their TeamUp email and their GitHub account rarely match, so this is how a
-- commit, a pull request or a review is known to be theirs.
CREATE TABLE IF NOT EXISTS code_identities (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider    VARCHAR(20) NOT NULL CHECK (provider IN ('github', 'bitbucket')),
    external_id VARCHAR(128) NOT NULL,
    login       VARCHAR(255) NOT NULL,
    -- The addresses they commit with, as the provider has verified them.
    emails      TEXT[] NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, provider),
    UNIQUE (org_id, provider, external_id)
);
CREATE INDEX IF NOT EXISTS idx_code_identities_login ON code_identities (org_id, provider, LOWER(login));

-- A repository a connection can see, and the project it belongs to, if any.
CREATE TABLE IF NOT EXISTS code_repositories (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    connection_id  UUID NOT NULL REFERENCES code_connections(id) ON DELETE CASCADE,
    provider       VARCHAR(20) NOT NULL,
    external_id    VARCHAR(128) NOT NULL,
    full_name      VARCHAR(512) NOT NULL,
    html_url       TEXT NOT NULL DEFAULT '',
    private        BOOLEAN NOT NULL DEFAULT TRUE,
    project_id     UUID REFERENCES projects(id) ON DELETE SET NULL,
    -- Still visible to the connection; a repository removed from the
    -- installation is kept for its history but no longer offered.
    present        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (connection_id, external_id)
);
CREATE INDEX IF NOT EXISTS idx_code_repositories_project ON code_repositories (project_id);

CREATE TABLE IF NOT EXISTS code_pull_requests (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repository_id UUID NOT NULL REFERENCES code_repositories(id) ON DELETE CASCADE,
    number        INT NOT NULL,
    title         VARCHAR(1024) NOT NULL DEFAULT '',
    -- open, draft, merged or closed.
    state         VARCHAR(20) NOT NULL,
    html_url      TEXT NOT NULL DEFAULT '',
    head_branch   VARCHAR(512) NOT NULL DEFAULT '',
    base_branch   VARCHAR(512) NOT NULL DEFAULT '',
    author_login  VARCHAR(255) NOT NULL DEFAULT '',
    author_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    -- passing, failing or pending, from the checks on its latest commit.
    checks        VARCHAR(20) NOT NULL DEFAULT '',
    opened_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    merged_at     TIMESTAMPTZ,
    closed_at     TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, number)
);

-- Who was asked to review, and what they said: requested, approved,
-- changes_requested or commented.
CREATE TABLE IF NOT EXISTS code_pull_request_reviewers (
    pull_request_id UUID NOT NULL REFERENCES code_pull_requests(id) ON DELETE CASCADE,
    login           VARCHAR(255) NOT NULL,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    state           VARCHAR(30) NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pull_request_id, login)
);

CREATE TABLE IF NOT EXISTS code_commits (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    repository_id UUID NOT NULL REFERENCES code_repositories(id) ON DELETE CASCADE,
    sha           VARCHAR(64) NOT NULL,
    message       TEXT NOT NULL DEFAULT '',
    branch        VARCHAR(512) NOT NULL DEFAULT '',
    html_url      TEXT NOT NULL DEFAULT '',
    author_login  VARCHAR(255) NOT NULL DEFAULT '',
    author_email  VARCHAR(320) NOT NULL DEFAULT '',
    author_name   VARCHAR(255) NOT NULL DEFAULT '',
    author_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    committed_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (repository_id, sha)
);
CREATE INDEX IF NOT EXISTS idx_code_commits_author ON code_commits (author_id, committed_at DESC);

-- A pull request or commit that names a task.
CREATE TABLE IF NOT EXISTS task_code_links (
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    pull_request_id UUID REFERENCES code_pull_requests(id) ON DELETE CASCADE,
    commit_id       UUID REFERENCES code_commits(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((pull_request_id IS NULL) <> (commit_id IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_code_links_pr ON task_code_links (task_id, pull_request_id) WHERE pull_request_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_code_links_commit ON task_code_links (task_id, commit_id) WHERE commit_id IS NOT NULL;

-- Each event a provider sent, once: they are retried, and must not be acted on twice.
CREATE TABLE IF NOT EXISTS code_deliveries (
    connection_id UUID NOT NULL REFERENCES code_connections(id) ON DELETE CASCADE,
    delivery_id   VARCHAR(128) NOT NULL,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (connection_id, delivery_id)
);
