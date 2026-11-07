-- A meeting nobody has held yet means nothing without these columns, and would be
-- listed as a call that never started. Calls that did happen stay as history.
DELETE FROM calls
WHERE scheduled_start IS NOT NULL AND standing_meeting_id IS NULL AND status IN ('scheduled', 'cancelled');

DROP INDEX IF EXISTS idx_calls_scheduled_start;
DROP INDEX IF EXISTS idx_calls_one_occurrence_per_day;
ALTER TABLE calls
    DROP COLUMN IF EXISTS occurrence_date,
    DROP COLUMN IF EXISTS standing_meeting_id,
    DROP COLUMN IF EXISTS scheduled_end,
    DROP COLUMN IF EXISTS scheduled_start;
DROP TABLE IF EXISTS standing_meetings;
