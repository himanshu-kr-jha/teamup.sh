-- Scrums saved before this have no record of who was added to what, and that
-- cannot be recovered, so they read as zero rather than being rewritten.
ALTER TABLE scrum_participants ADD COLUMN IF NOT EXISTS added_to INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scrum_entries ADD COLUMN IF NOT EXISTS added_to BOOLEAN NOT NULL DEFAULT FALSE;
