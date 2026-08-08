## Question

Design the core database schema: organizations, users, roles (RBAC with custom permissions), teams/departments (hierarchy), channels (DM, group, public), messages (with search vector), tasks (Jira-like: status, priority, assignee, reporter, sprint, labels, comments, attachments), calls (LiveKit room mapping, participants, recording ref), presence, push subscriptions, audit log. Define indexes, foreign keys, and migration strategy (golang-migrate).

Status: claimed

## Answer

**Decision: Core database schema defined below.**

### 1. Migration Strategy (golang-migrate)

```
migrations/
├── 0001_initial_schema.up.sql
├── 0001_initial_schema.down.sql
├── 0002_add_search_vectors.up.sql
├── 0002_add_search_vectors.down.sql
├── 0003_attachments.up.sql
├── 0003_attachments.down.sql
├── 0004_messages_search.up.sql
├── 0004_messages_search.down.sql
├── 0005_push_subscriptions.up.sql
├── 0005_push_subscriptions.down.sql
├── 0006_notifications.up.sql
├── 0006_notifications.down.sql
├── 0007_audit_log.up.sql
├── 0007_audit_log.down.sql
└── 0008_task_enhancements.up.sql
```

**Go Integration:**
```go
// internal/database/migrate.go
import "github.com/golang-migrate/migrate/v4"

func RunMigrations(databaseURL string) error {
    m, err := migrate.New("file://migrations", databaseURL)
    if err != nil { return err }
    return m.Up()
}

// CLI command: teamup migrate up|down|version|force
```

---

### 2. Complete Schema (PostgreSQL 16+)

```sql
-- 0001_initial_schema.up.sql

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
    subdomain       VARCHAR(63) NOT NULL UNIQUE,  -- RFC 1123, for org.teamup.local
    logo_url        TEXT,
    settings        JSONB DEFAULT '{}',           -- Feature flags, limits, theme
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
    password_hash   VARCHAR(255),                 -- NULL for SSO-only users
    avatar_url      TEXT,
    role            VARCHAR(50) DEFAULT 'member', -- 'owner' | 'admin' | 'manager' | 'member'
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
    permissions     JSONB NOT NULL DEFAULT '[]',  -- ["chat:read", "chat:write", "tasks:manage", ...]
    is_system       BOOLEAN DEFAULT FALSE,        -- Cannot delete system roles
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (org_id, name)
);

CREATE INDEX idx_roles_org ON roles(org_id);

-- Default system roles (inserted in migration)
-- owner: ["*"]
-- admin: ["org:manage", "users:manage", "roles:manage", "channels:manage", "tasks:manage", "calls:manage", "settings:manage"]
-- manager: ["channels:read", "channels:write", "tasks:manage", "calls:manage", "reports:read"]
-- member: ["channels:read", "channels:write", "tasks:read", "tasks:write:self", "calls:join"]

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
    parent_id       UUID REFERENCES teams(id) ON DELETE SET NULL,  -- Hierarchy
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

-- Team members
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
    team_id         UUID REFERENCES teams(id) ON DELETE SET NULL,  -- Optional team association
    name            VARCHAR(255),
    type            VARCHAR(20) NOT NULL DEFAULT 'group',  -- 'dm' | 'group' | 'public' | 'announcement'
    description     TEXT,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    is_archived     BOOLEAN DEFAULT FALSE,
    is_private      BOOLEAN DEFAULT TRUE,                  -- Public channels: false
    max_members     INT DEFAULT 1000,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_channels_org ON channels(org_id) WHERE deleted_at IS NULL AND is_archived = FALSE;
CREATE INDEX idx_channels_team ON channels(team_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_channels_type ON channels(type) WHERE deleted_at IS NULL;

-- Channel members
CREATE TABLE channel_members (
    channel_id      UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            VARCHAR(20) DEFAULT 'member',  -- 'owner' | 'admin' | 'member'
    joined_at       TIMESTAMPTZ DEFAULT NOW(),
    last_read_at    TIMESTAMPTZ,
    notify_level    VARCHAR(20) DEFAULT 'all',     -- 'all' | 'mentions' | 'none'
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

-- Message reactions
CREATE TABLE message_reactions (
    message_id      UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji           VARCHAR(50) NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (message_id, user_id, emoji)
);

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
    status          VARCHAR(30) NOT NULL DEFAULT 'todo',  -- 'todo' | 'in_progress' | 'in_review' | 'done' | 'blocked'
    priority        VARCHAR(20) NOT NULL DEFAULT 'medium', -- 'low' | 'medium' | 'high' | 'urgent'
    task_type       VARCHAR(20) NOT NULL DEFAULT 'task',   -- 'task' | 'bug' | 'story' | 'epic' | 'subtask'
    story_points    INT,
    due_date        TIMESTAMPTZ,
    start_date      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    parent_task_id  UUID REFERENCES tasks(id) ON DELETE SET NULL,  -- For subtasks
    position        INT DEFAULT 0,                              -- For board ordering
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

-- Task labels (many-to-many)
CREATE TABLE labels (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(100) NOT NULL,
    color           VARCHAR(7) DEFAULT '#6366f1',  -- Hex color
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (org_id, name)
);

CREATE TABLE task_labels (
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    label_id        UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, label_id)
);

-- Task comments
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

-- Task attachments (ref to attachments table)
CREATE TABLE task_attachments (
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    attachment_id   UUID NOT NULL REFERENCES attachments(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, attachment_id)
);

-- Task watchers
CREATE TABLE task_watchers (
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, user_id)
);

-- Task activity log (simplified audit for tasks)
CREATE TABLE task_activities (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    field           VARCHAR(50) NOT NULL,      -- 'status', 'assignee', 'priority', etc.
    old_value       TEXT,
    new_value       TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_task_activities_task ON task_activities(task_id, created_at DESC);

-- ──────────────────────────────────────────────
-- Projects (for task grouping)
-- ──────────────────────────────────────────────
CREATE TABLE projects (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    key             VARCHAR(10) NOT NULL,       -- Short prefix: "PROJ", "TASK"
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
    status          VARCHAR(20) DEFAULT 'planned',  -- 'planned' | 'active' | 'completed'
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_sprints_project ON sprints(project_id);

-- Sprint tasks (many-to-many)
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
    channel_id      UUID REFERENCES channels(id) ON DELETE SET NULL,  -- Optional: call in channel context
    room_name       VARCHAR(255) NOT NULL,        -- LiveKit room name
    livekit_room_sid VARCHAR(255),                -- LiveKit room SID (after creation)
    title           VARCHAR(500),
    call_type       VARCHAR(20) NOT NULL DEFAULT 'video',  -- 'video' | 'audio'
    status          VARCHAR(20) NOT NULL DEFAULT 'scheduled', -- 'scheduled' | 'active' | 'ended' | 'cancelled'
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    duration_seconds INT,
    recording_url   TEXT,
    recording_status VARCHAR(20),                 -- 'recording' | 'completed' | 'failed'
    created_by      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_calls_org ON calls(org_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_calls_channel ON calls(channel_id);
CREATE INDEX idx_calls_status ON calls(status);
CREATE INDEX idx_calls_room ON calls(room_name);
CREATE INDEX idx_calls_time ON calls(started_at DESC);

-- Call participants
CREATE TABLE call_participants (
    call_id         UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            VARCHAR(20) DEFAULT 'participant',  -- 'host' | 'participant' | 'moderator'
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
    status          VARCHAR(20) NOT NULL DEFAULT 'offline',  -- 'online' | 'away' | 'busy' | 'in_call' | 'offline'
    custom_status   TEXT,
    custom_status_emoji VARCHAR(50),
    last_activity_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_presence_org_status ON user_presence(org_id, status);

-- ──────────────────────────────────────────────
-- Push Subscriptions (Web Push)
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
-- Attachments
-- ──────────────────────────────────────────────
CREATE TABLE attachments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    uploaded_by     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category        VARCHAR(20) NOT NULL,  -- 'chat' | 'tasks' | 'recordings' | 'avatars' | 'exports'
    ref_id          VARCHAR(255) NOT NULL, -- channel_id, task_id, room_name, user_id, export_id
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
-- Notifications (In-App)
-- ──────────────────────────────────────────────
CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type            VARCHAR(50) NOT NULL,   -- 'chat.message', 'call.invite', 'task.assigned', 'mention', 'task.due'
    title           VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    data            JSONB,                  -- { channelId, messageId, roomName, taskId, ... }
    read_at         TIMESTAMPTZ,
    archived_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_unread ON notifications(user_id, read_at) WHERE read_at IS NULL;
CREATE INDEX idx_notifications_user_created ON notifications(user_id, created_at DESC);

-- ──────────────────────────────────────────────
-- Org Configs (Key-Value, encrypted for secrets)
-- ──────────────────────────────────────────────
CREATE TABLE org_configs (
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key             VARCHAR(100) NOT NULL,
    value           JSONB NOT NULL,
    is_secret       BOOLEAN DEFAULT FALSE,  -- Encrypted at rest
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (org_id, key)
);

-- ──────────────────────────────────────────────
-- Audit Log
-- ──────────────────────────────────────────────
CREATE TABLE audit_logs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    action          VARCHAR(100) NOT NULL,    -- 'user.create', 'task.update', 'settings.change', etc.
    resource_type   VARCHAR(50),              -- 'user', 'task', 'channel', 'org', etc.
    resource_id     UUID,
    old_value       JSONB,
    new_value       JSONB,
    ip_address      INET,
    user_agent      TEXT,
    correlation_id  UUID,                     -- Links to request correlation ID
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_org_time ON audit_logs(org_id, created_at DESC);
CREATE INDEX idx_audit_user_time ON audit_logs(user_id, created_at DESC);
CREATE INDEX idx_audit_resource ON audit_logs(resource_type, resource_id);
CREATE INDEX idx_audit_correlation ON audit_logs(correlation_id);
CREATE INDEX idx_audit_action ON audit_logs(action);

-- Partition by month for performance (PostgreSQL 12+)
-- CREATE TABLE audit_logs_2024_01 PARTITION OF audit_logs FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');
```

---

### 3. Key Design Decisions

| Aspect | Decision |
|--------|----------|
| **UUIDs** | All PKs are UUIDv4 (`uuid_generate_v4()`) — globally unique, no collision risk |
| **Soft Deletes** | `deleted_at` timestamp on all core tables — enables recovery, audit trail |
| **Org Isolation** | Every table has `org_id` FK — row-level security via `WHERE org_id = $1` |
| **RBAC** | Roles + permissions JSONB — flexible, no schema changes for new permissions |
| **Hierarchy** | Teams self-referential `parent_id` — recursive CTEs for tree queries |
| **Search** | Generated `tsvector` column on messages — automatic, consistent |
| **Tasks** | Jira-like: status, priority, type, story points, sprints, labels, subtasks, comments, attachments, watchers, activity log |
| **Calls** | LiveKit room mapping + participants + recording tracking |
| **Attachments** | Single table, polymorphic via `category` + `ref_id` — unified storage |
| **Audit** | Comprehensive audit log with correlation IDs — compliance ready |

---

### 4. Migration Tooling (golang-migrate)

```go
// cmd/teamup/migrate.go
var migrateCmd = &cobra.Command{
    Use:   "migrate [up|down|version|force|create]",
    Short: "Database migrations",
    RunE: func(cmd *cobra.Command, args []string) error {
        dbURL := buildDatabaseURL(cfg)
        m, err := migrate.New("file://migrations", dbURL)
        if err != nil { return err }

        switch args[0] {
        case "up": return m.Up()
        case "down": return m.Down()
        case "version":
            v, d, _ := m.Version()
            fmt.Printf("Version: %d, Dirty: %v\n", v, d)
        case "force":
            version, _ := strconv.Atoi(args[1])
            return m.Force(version)
        case "create":
            name := strings.Join(args[1:], "_")
            return createMigrationFiles(name)
        }
        return nil
    },
}

func createMigrationFiles(name string) error {
    // Creates timestamped up/down SQL files
}
```

---

### 5. Row Level Security (Optional, v2)

```sql
-- Enable RLS for multi-tenant isolation at DB level
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
CREATE POLICY users_org_isolation ON users
    USING (org_id = current_setting('app.current_org_id')::UUID);
-- Set via: SET app.current_org_id = 'org-uuid';
```

**v1 Decision:** Application-level enforcement (all queries include `org_id`). RLS in v2 for defense-in-depth.

---

### 6. Open Questions (for later)

- **Partitioning** for messages, audit_logs (by month) — v2
- **Read replicas** for analytics queries — v2
- **CDC** (Change Data Capture) for real-time sync — v2
- **Multi-region** database — v3

Status: resolved
