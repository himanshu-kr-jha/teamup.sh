-- A permanent room for the whole organization. One day of it is one row in calls.
CREATE TABLE IF NOT EXISTS standing_meetings (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             VARCHAR(255) NOT NULL,
    time_of_day      VARCHAR(5) NOT NULL,
    time_zone        VARCHAR(100) NOT NULL,
    weekdays         SMALLINT[] NOT NULL,
    duration_minutes INTEGER NOT NULL DEFAULT 30,
    created_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    archived_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_standing_meetings_org ON standing_meetings (org_id) WHERE archived_at IS NULL;

-- A scheduled meeting and an occurrence both know when they were meant to happen,
-- which an unscheduled call does not. An occurrence copies its start in, so
-- changing the meeting's time later does not rewrite how late people were.
ALTER TABLE calls
    ADD COLUMN IF NOT EXISTS scheduled_start     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS scheduled_end       TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS standing_meeting_id UUID REFERENCES standing_meetings(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS occurrence_date     DATE;

-- This is what makes two people joining at the same moment one occurrence.
CREATE UNIQUE INDEX IF NOT EXISTS idx_calls_one_occurrence_per_day
    ON calls (standing_meeting_id, occurrence_date) WHERE standing_meeting_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_calls_scheduled_start
    ON calls (scheduled_start) WHERE scheduled_start IS NOT NULL;
