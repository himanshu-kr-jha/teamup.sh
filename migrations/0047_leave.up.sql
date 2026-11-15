-- Leave is dated, so attendance can tell a day somebody was away on leave from a
-- day they simply did not come. A record is removed by marking it, never by
-- deleting it, and every change is kept in leave_changes, so turning a past
-- absence into leave always shows who did it and when.
CREATE TABLE IF NOT EXISTS leave_periods (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    starts_on   DATE NOT NULL,
    -- NULL is leave with no return date yet: "on leave" chosen with no "back on".
    ends_on     DATE,
    -- A half day is a single date, the morning or the afternoon of it.
    part        VARCHAR(16) NOT NULL DEFAULT 'full' CHECK (part IN ('full', 'morning', 'afternoon')),
    note        TEXT NOT NULL DEFAULT '',
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    removed_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    removed_at  TIMESTAMPTZ,
    CHECK (ends_on IS NULL OR ends_on >= starts_on),
    CHECK (part = 'full' OR ends_on = starts_on)
);
CREATE INDEX IF NOT EXISTS idx_leave_periods_person ON leave_periods (org_id, user_id, starts_on) WHERE removed_at IS NULL;

-- What a leave record was after each change, and who made it.
CREATE TABLE IF NOT EXISTS leave_changes (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    leave_id   UUID NOT NULL REFERENCES leave_periods(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    actor_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    action     VARCHAR(16) NOT NULL CHECK (action IN ('added', 'changed', 'removed')),
    starts_on  DATE NOT NULL,
    ends_on    DATE,
    part       VARCHAR(16) NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_leave_changes_leave ON leave_changes (leave_id, created_at);

-- A day the whole organization is off. Nobody is absent from anything on it.
CREATE TABLE IF NOT EXISTS org_holidays (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    on_date    DATE NOT NULL,
    name       VARCHAR(120) NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, on_date)
);
