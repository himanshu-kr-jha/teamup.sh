ALTER TABLE call_participants DROP COLUMN IF EXISTS answered_at;
DROP TABLE IF EXISTS call_events;
DROP TABLE IF EXISTS call_sessions;
