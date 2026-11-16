-- Attendance rules, the owner's decisions of 16 November 2026.

-- A stand-up may require cameras: whoever joined and never turned theirs on is
-- in review rather than present. Each day's call keeps the rule as it was that
-- day, so changing it later does not rewrite past attendance.
ALTER TABLE standing_meetings ADD COLUMN IF NOT EXISTS camera_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE calls ADD COLUMN IF NOT EXISTS camera_required BOOLEAN NOT NULL DEFAULT FALSE;

-- A camera turned on is on the call's trail, which is how the rule is read.
ALTER TABLE call_events DROP CONSTRAINT IF EXISTS call_events_type_check;
ALTER TABLE call_events ADD CONSTRAINT call_events_type_check CHECK (type IN (
    'knocked', 'admitted', 'denied', 'joined', 'left',
    'screen_share_started', 'screen_share_stopped', 'camera_on'));

-- Working in the field: a photo taken in the app at the moment, where the phone
-- was then, and an optional note. It counts for stand-ups on its date.
CREATE TABLE IF NOT EXISTS field_checkins (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The person's own date when they checked in.
    on_date      DATE NOT NULL,
    taken_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    latitude     DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude    DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    accuracy_m   REAL NOT NULL CHECK (accuracy_m >= 0),
    note         TEXT NOT NULL DEFAULT '',
    photo_type   VARCHAR(32) NOT NULL,
    photo        BYTEA NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_field_checkins_day ON field_checkins (org_id, on_date, user_id);

-- A reviewer's decision about one person on one day of a stand-up. Kept with
-- who made it and why; a new decision replaces the old.
CREATE TABLE IF NOT EXISTS attendance_reviews (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id              UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    standing_meeting_id UUID NOT NULL REFERENCES standing_meetings(id) ON DELETE CASCADE,
    on_date             DATE NOT NULL,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    decision            VARCHAR(16) NOT NULL CHECK (decision IN ('present', 'absent')),
    note                TEXT NOT NULL DEFAULT '',
    decided_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (standing_meeting_id, on_date, user_id)
);

-- Reviewing attendance is a permission of its own, to give to an HR role.
-- Administrators have it; the owner has everything already.
UPDATE roles SET permissions = permissions || '["attendance.review"]'::jsonb, updated_at = NOW()
WHERE is_system AND name = 'admin' AND NOT permissions ? 'attendance.review';
