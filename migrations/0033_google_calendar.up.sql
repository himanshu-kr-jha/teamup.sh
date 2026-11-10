-- Somebody's Google Calendar, linked to their TeamUp account. The token is
-- sealed with TEAMUP_GCAL_TOKEN_KEY, so a database backup cannot read anybody's
-- Google Calendar on its own.
CREATE TABLE IF NOT EXISTS google_calendar_links (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    google_email   VARCHAR(320) NOT NULL DEFAULT '',
    token          BYTEA NOT NULL,
    -- ok, or reconnect when Google no longer accepts the token.
    status         VARCHAR(16) NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'reconnect')),
    last_synced_at TIMESTAMPTZ,
    last_error     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Meetings from somebody's Google Calendar, as last read. Busy time on their
-- TeamUp calendar; the name is theirs and their managers' to read.
CREATE TABLE IF NOT EXISTS google_calendar_events (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    google_id  VARCHAR(1024) NOT NULL,
    title      VARCHAR(1024) NOT NULL DEFAULT '',
    starts_at  TIMESTAMPTZ NOT NULL,
    ends_at    TIMESTAMPTZ NOT NULL,
    private    BOOLEAN NOT NULL DEFAULT FALSE,
    html_link  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, google_id),
    CHECK (ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS idx_google_calendar_events_time ON google_calendar_events (user_id, starts_at);

-- The TeamUp meetings put into somebody's Google Calendar, so a moved or
-- cancelled meeting changes or leaves it too.
CREATE TABLE IF NOT EXISTS google_calendar_pushed (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    call_id         UUID NOT NULL,
    google_event_id VARCHAR(1024) NOT NULL,
    -- What was sent, so an unchanged meeting is not sent again.
    fingerprint     TEXT NOT NULL,
    PRIMARY KEY (user_id, call_id)
);
