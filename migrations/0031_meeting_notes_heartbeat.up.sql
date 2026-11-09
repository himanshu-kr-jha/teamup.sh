-- When the note-taker last said it was still at work on a call's notes. A
-- server that stops hands its calls over by clearing it; one that dies stops
-- renewing it. Either way another server sees the notes are nobody's and
-- carries on with them, instead of leaving them half taken for good.
ALTER TABLE meeting_notes ADD COLUMN heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
