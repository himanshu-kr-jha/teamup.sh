DROP TABLE IF EXISTS call_notes_changes;
ALTER TABLE standing_meetings DROP COLUMN IF EXISTS notes_on;
ALTER TABLE calls DROP COLUMN IF EXISTS notes_on;
