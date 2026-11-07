-- Time somebody has blocked for their own work, or marked as free for meetings.
CREATE TABLE IF NOT EXISTS calendar_blocks (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       VARCHAR(10) NOT NULL CHECK (kind IN ('focus', 'open')),
    title      VARCHAR(255) NOT NULL DEFAULT '',
    starts_at  TIMESTAMPTZ NOT NULL,
    ends_at    TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS idx_calendar_blocks_user ON calendar_blocks (user_id, starts_at);

-- The start a reminder was last claimed for. A sweep claims by setting this, and
-- moving the meeting makes it differ again, so the new time is reminded too.
ALTER TABLE calls ADD COLUMN IF NOT EXISTS reminded_for TIMESTAMPTZ;
