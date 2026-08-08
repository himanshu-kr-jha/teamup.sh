-- Migration: initial_schema
-- Created: 2024-01-15T00:00:00Z

-- ──────────────────────────────────────────────
-- Extensions
-- ──────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "btree_gin";

-- ──────────────────────────────────────────────
-- Organizations
-- ──────────────────────────────────────────────
CREATE TABLE organizations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name            VARCHAR(255) NOT NULL,
    subdomain       VARCHAR(63) NOT NULL UNIQUE,
    logo_url        TEXT,
    settings        JSONB DEFAULT '{}',
    is_active       BOOLEAN DEFAULT TRUE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_orgs_subdomain ON organizations(subdomain) WHERE deleted_at IS NULL;
CREATE INDEX idx_orgs_active ON organizations(is_active) WHERE deleted_at IS NULL;

-- ──────────────────────────────────────────────
-- Users
-- ──────────────────────────────────────────────
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    email           VARCHAR(255) NOT NULL,
    password_hash   VARCHAR(255),
    avatar_url      TEXT,
    role            VARCHAR(50) DEFAULT 'member',
    is_admin        BOOLEAN DEFAULT FALSE,
    is_active       BOOLEAN DEFAULT TRUE,
    last_seen_at    TIMESTAMPTZ,
    timezone        VARCHAR(50) DEFAULT 'UTC',
    locale          VARCHAR(10) DEFAULT 'en',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    UNIQUE (org_id, email)
);

CREATE INDEX idx_users_org ON users(org_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_email ON users(email) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_active ON users(org_id, is_active) WHERE deleted_at IS NULL;

-- ──────────────────────────────────────────────
-- Roles & Permissions (RBAC)
-- ──────────────────────────────────────────────
CREATE TABLE roles (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(100) NOT NULL,
    description     TEXT,
    permissions     JSONB NOT NULL DEFAULT '[]',
    is_system       BOOLEAN DEFAULT FALSE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (org_id, name)
);

CREATE INDEX idx_roles_org ON roles(org_id);

-- ──────────────────────────────────────────────
-- User Roles (many-to-many)
-- ──────────────────────────────────────────────
CREATE TABLE user_roles (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id         UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    assigned_by     UUID REFERENCES users(id),
    assigned_at     TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX idx_user_roles_user ON user_roles(user_id);
CREATE INDEX idx_user_roles_role ON user_roles(role_id);

-- ──────────────────────────────────────────────
-- Teams / Departments (Hierarchy)
-- ──────────────────────────────────────────────
CREATE TABLE teams (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_id       UUID REFERENCES teams(id) ON DELETE SET NULL,
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    manager_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    UNIQUE (org_id, name)
);

CREATE INDEX idx_teams_org ON teams(org_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_teams_parent ON teams(parent_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_teams_manager ON teams(manager_id);

CREATE TABLE team_members (
    team_id         UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at       TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (team_id, user_id)
);

-- ──────────────────────────────────────────────
-- Channels
-- ──────────────────────────────────────────────
CREATE TABLE channels (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    team_id         UUID REFERENCES teams(id) ON DELETE SET NULL,
    name            VARCHAR(255),
    type            VARCHAR(20) NOT NULL DEFAULT 'group',
    description     TEXT,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    is_archived     BOOLEAN DEFAULT FALSE,
    is_private      BOOLEAN DEFAULT TRUE,
    max_members     INT DEFAULT 1000,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_channels_org ON channels(org_id) WHERE deleted_at IS NULL AND is_archived = FALSE;
CREATE INDEX idx_channels_team ON channels(team_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_channels_type ON channels(type) WHERE deleted_at IS NULL;

CREATE TABLE channel_members (
    channel_id      UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            VARCHAR(20) DEFAULT 'member',
    joined_at       TIMESTAMPTZ DEFAULT NOW(),
    last_read_at    TIMESTAMPTZ,
    notify_level    VARCHAR(20) DEFAULT 'all',
    PRIMARY KEY (channel_id, user_id)
);

CREATE INDEX idx_channel_members_user ON channel_members(user_id);

-- ──────────────────────────────────────────────
-- Messages
-- ──────────────────────────────────────────────
CREATE TABLE messages (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    channel_id      UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content         TEXT NOT NULL,
    content_search  tsvector GENERATED ALWAYS AS (to_tsvector('english', coalesce(content, ''))) STORED,
    reply_to        UUID REFERENCES messages(id) ON DELETE SET NULL,
    edited_at       TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_messages_channel_time ON messages(channel_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_messages_user ON messages(user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_messages_reply ON messages(reply_to) WHERE reply_to IS NOT NULL;
CREATE INDEX idx_messages_search ON messages USING GIN (content_search);

CREATE TABLE message_reactions (
    message_id      UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji           VARCHAR(50) NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (message_id, user_id, emoji)
);

-- ──────────────────────────────────────────────
-- Projects
-- ──────────────────────────────────────────────
CREATE TABLE projects (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    key             VARCHAR(10) NOT NULL,
    description     TEXT,
    lead_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    is_active       BOOLEAN DEFAULT TRUE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    UNIQUE (org_id, key)
);

CREATE INDEX idx_projects_org ON projects(org_id) WHERE deleted_at IS NULL;

-- ──────────────────────────────────────────────
-- Sprints
-- ──────────────────────────────────────────────
CREATE TABLE sprints (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    goal            TEXT,
    start_date      TIMESTAMPTZ NOT NULL,
    end_date        TIMESTAMPTZ NOT NULL,
    status          VARCHAR(20) DEFAULT 'planned',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_sprints_project ON sprints(project_id);

-- ──────────────────────────────────────────────
-- Attachments (moved before tasks for FK references)
-- ──────────────────────────────────────────────
CREATE TABLE attachments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    uploaded_by     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category        VARCHAR(20) NOT NULL,
    ref_id          VARCHAR(255) NOT NULL,
    object_key      VARCHAR(1024) NOT NULL UNIQUE,
    filename        VARCHAR(512) NOT NULL,
    content_type    VARCHAR(128) NOT NULL,
    size_bytes      BIGINT NOT NULL,
    thumbnail_key   VARCHAR(1024),
    checksum_sha256 CHAR(64),
    virus_scanned   BOOLEAN DEFAULT FALSE,
    virus_clean     BOOLEAN,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_attachments_org_cat_ref ON attachments(org_id, category, ref_id);
CREATE INDEX idx_attachments_object_key ON attachments(object_key);
CREATE INDEX idx_attachments_uploaded_by ON attachments(uploaded_by);

-- ──────────────────────────────────────────────
-- Tasks (Jira-like)
-- ──────────────────────────────────────────────
CREATE TABLE tasks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE SET NULL,
    reporter_id     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    assignee_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    title           VARCHAR(500) NOT NULL,
    description     TEXT,
    status          VARCHAR(30) NOT NULL DEFAULT 'todo',
    priority        VARCHAR(20) NOT NULL DEFAULT 'medium',
    task_type       VARCHAR(20) NOT NULL DEFAULT 'task',
    story_points    INT,
    due_date        TIMESTAMPTZ,
    start_date      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    parent_task_id  UUID REFERENCES tasks(id) ON DELETE SET NULL,
    position        INT DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_tasks_org ON tasks(org_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_project ON tasks(project_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_assignee ON tasks(assignee_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_status ON tasks(status) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_reporter ON tasks(reporter_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_parent ON tasks(parent_task_id) WHERE parent_task_id IS NOT NULL;
CREATE INDEX idx_tasks_due ON tasks(due_date) WHERE due_date IS NOT NULL AND deleted_at IS NULL;

CREATE TABLE labels (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(100) NOT NULL,
    color           VARCHAR(7) DEFAULT '#6366f1',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (org_id, name)
);

CREATE TABLE task_labels (
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    label_id        UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, label_id)
);

CREATE TABLE task_comments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_task_comments_task ON task_comments(task_id, created_at);

CREATE TABLE task_attachments (
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    attachment_id   UUID NOT NULL REFERENCES attachments(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, attachment_id)
);

CREATE TABLE task_watchers (
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, user_id)
);

CREATE TABLE task_activities (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    field           VARCHAR(50) NOT NULL,
    old_value       TEXT,
    new_value       TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_task_activities_task ON task_activities(task_id, created_at DESC);

-- ──────────────────────────────────────────────
-- Sprint Tasks (junction table - after tasks table)
-- ──────────────────────────────────────────────
CREATE TABLE sprint_tasks (
    sprint_id       UUID NOT NULL REFERENCES sprints(id) ON DELETE CASCADE,
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (sprint_id, task_id)
);

-- ──────────────────────────────────────────────
-- Calls (LiveKit integration)
-- ──────────────────────────────────────────────
CREATE TABLE calls (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    channel_id      UUID REFERENCES channels(id) ON DELETE SET NULL,
    room_name       VARCHAR(255) NOT NULL,
    livekit_room_sid VARCHAR(255),
    title           VARCHAR(500),
    call_type       VARCHAR(20) NOT NULL DEFAULT 'video',
    status          VARCHAR(20) NOT NULL DEFAULT 'scheduled',
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    duration_seconds INT,
    recording_url   TEXT,
    recording_status VARCHAR(20),
    created_by      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_calls_org ON calls(org_id);
CREATE INDEX idx_calls_channel ON calls(channel_id);
CREATE INDEX idx_calls_status ON calls(status);
CREATE INDEX idx_calls_room ON calls(room_name);
CREATE INDEX idx_calls_time ON calls(started_at DESC);

CREATE TABLE call_participants (
    call_id         UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            VARCHAR(20) DEFAULT 'participant',
    joined_at       TIMESTAMPTZ,
    left_at         TIMESTAMPTZ,
    is_muted        BOOLEAN DEFAULT FALSE,
    is_camera_on    BOOLEAN DEFAULT TRUE,
    PRIMARY KEY (call_id, user_id)
);

-- ──────────────────────────────────────────────
-- Presence
-- ──────────────────────────────────────────────
CREATE TABLE user_presence (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    status          VARCHAR(20) NOT NULL DEFAULT 'offline',
    custom_status   TEXT,
    custom_status_emoji VARCHAR(50),
    last_activity_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_presence_org_status ON user_presence(org_id, status);

-- ──────────────────────────────────────────────
-- Push Subscriptions
-- ──────────────────────────────────────────────
CREATE TABLE push_subscriptions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint        TEXT NOT NULL,
    p256dh          TEXT NOT NULL,
    auth            TEXT NOT NULL,
    user_agent      TEXT,
    device_name     TEXT,
    last_used_at    TIMESTAMPTZ DEFAULT NOW(),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (user_id, endpoint)
);

CREATE INDEX idx_push_subs_user ON push_subscriptions(user_id);
CREATE INDEX idx_push_subs_org ON push_subscriptions(org_id);

-- ──────────────────────────────────────────────
-- Notifications (In-App)
-- ──────────────────────────────────────────────
CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type            VARCHAR(50) NOT NULL,
    title           VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    data            JSONB,
    read_at         TIMESTAMPTZ,
    archived_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_unread ON notifications(user_id, read_at) WHERE read_at IS NULL;
CREATE INDEX idx_notifications_user_created ON notifications(user_id, created_at DESC);

-- ──────────────────────────────────────────────
-- Org Configs
-- ──────────────────────────────────────────────
CREATE TABLE org_configs (
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key             VARCHAR(100) NOT NULL,
    value           JSONB NOT NULL,
    is_secret       BOOLEAN DEFAULT FALSE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (org_id, key)
);

-- ──────────────────────────────────────────────
-- Refresh Tokens
-- ──────────────────────────────────────────────
CREATE TABLE refresh_tokens (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id      VARCHAR(255) NOT NULL,
    token_hash      TEXT NOT NULL,
    user_agent      TEXT,
    ip_address      INET,
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_session ON refresh_tokens(session_id);
CREATE INDEX idx_refresh_tokens_expires ON refresh_tokens(expires_at) WHERE revoked_at IS NULL;

-- ──────────────────────────────────────────────
-- Linked Identities
-- ──────────────────────────────────────────────
CREATE TABLE linked_identities (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider        VARCHAR(50) NOT NULL,
    provider_id     VARCHAR(255) NOT NULL,
    email           VARCHAR(255),
    access_token    TEXT,
    refresh_token   TEXT,
    id_token        TEXT,
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, provider)
);

-- ──────────────────────────────────────────────
-- Audit Logs
-- ──────────────────────────────────────────────
CREATE TABLE audit_logs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    action          VARCHAR(100) NOT NULL,
    resource_type   VARCHAR(50),
    resource_id     UUID,
    old_value       JSONB,
    new_value       JSONB,
    ip_address      INET,
    user_agent      TEXT,
    correlation_id  UUID,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_org_time ON audit_logs(org_id, created_at DESC);
CREATE INDEX idx_audit_user_time ON audit_logs(user_id, created_at DESC);
CREATE INDEX idx_audit_resource ON audit_logs(resource_type, resource_id);
CREATE INDEX idx_audit_correlation ON audit_logs(correlation_id);
CREATE INDEX idx_audit_action ON audit_logs(action);

-- ──────────────────────────────────────────────
-- Default Data
-- ──────────────────────────────────────────────

-- Insert default system roles (will be created per org in wizard)
-- This is just a reference; actual insertion happens in wizard finalization
