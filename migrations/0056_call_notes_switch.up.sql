-- Notes on or off for each call, and paused and resumed during one (the
-- owner's request of 18 November 2026).
--
-- A call's notes_on is what somebody chose: when it was made, or since, by
-- pausing or resuming in the call. Empty is the organization's default, which
-- is every call unless an administrator says only when somebody asks. A
-- standing meeting's is copied to each day's call.
ALTER TABLE calls ADD COLUMN IF NOT EXISTS notes_on BOOLEAN;
ALTER TABLE standing_meetings ADD COLUMN IF NOT EXISTS notes_on BOOLEAN;

-- Every time notes were turned on or off during a call: who, and when. The
-- minutes say from this where nothing was written down.
CREATE TABLE IF NOT EXISTS call_notes_changes (
    id         BIGSERIAL PRIMARY KEY,
    call_id    UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    notes_on   BOOLEAN NOT NULL,
    user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    user_name  TEXT NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_call_notes_changes_call ON call_notes_changes (call_id, changed_at, id);
