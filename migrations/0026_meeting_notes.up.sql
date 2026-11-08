-- The note-taker's record of a call: whether it is taking notes, writing the
-- minutes, done or failed, and the minutes themselves. One row per call, which
-- is also how a call is claimed by exactly one server.
CREATE TABLE IF NOT EXISTS meeting_notes (
    call_id     UUID PRIMARY KEY REFERENCES calls(id) ON DELETE CASCADE,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'taking',
    minutes     JSONB,
    error       TEXT,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

-- What was said, one stretch of one person's speech at a time, written as it is
-- transcribed so a crash during a meeting loses minutes and not the transcript.
CREATE TABLE IF NOT EXISTS meeting_note_segments (
    id           BIGSERIAL PRIMARY KEY,
    call_id      UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    user_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    speaker_name TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ NOT NULL,
    text         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meeting_note_segments_call ON meeting_note_segments(call_id, started_at);
