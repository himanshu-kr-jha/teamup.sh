-- Attendance comes from LiveKit's webhooks. A token is only permission.
--
-- One row per connection, keyed by LiveKit's participant sid, so dropping and
-- rejoining or using two devices keeps every session. joined_at is empty only
-- while a leave that overtook its join waits for it.
CREATE TABLE IF NOT EXISTS call_sessions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    call_id         UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    participant_sid VARCHAR(255) NOT NULL,
    joined_at       TIMESTAMPTZ,
    left_at         TIMESTAMPTZ,
    UNIQUE (call_id, participant_sid)
);
CREATE INDEX IF NOT EXISTS idx_call_sessions_user ON call_sessions (call_id, user_id);

-- The audit trail. Nothing in the application updates or deletes a row.
-- dedupe_key is what makes a retried webhook land once; events raised by a person
-- rather than by LiveKit leave it empty.
CREATE TABLE IF NOT EXISTS call_events (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    call_id    UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    type       VARCHAR(30) NOT NULL CHECK (type IN (
        'knocked', 'admitted', 'denied', 'joined', 'left',
        'screen_share_started', 'screen_share_stopped')),
    user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    at         TIMESTAMPTZ NOT NULL,
    dedupe_key VARCHAR(300),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (call_id, dedupe_key)
);
CREATE INDEX IF NOT EXISTS idx_call_events_call_at ON call_events (call_id, at);

-- That somebody asked for a token to answer a ring. It is not attendance: it is
-- what stops a phone ringing again for a call already picked up, and it has to
-- work when webhooks are not arriving, so it cannot be read from call_sessions.
ALTER TABLE call_participants ADD COLUMN IF NOT EXISTS answered_at TIMESTAMPTZ;
