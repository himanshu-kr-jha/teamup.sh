UPDATE calls SET missed_reason = 'no_answer' WHERE missed_reason = 'on_leave';
ALTER TABLE calls DROP CONSTRAINT IF EXISTS calls_missed_reason_check;
ALTER TABLE calls ADD CONSTRAINT calls_missed_reason_check
    CHECK (missed_reason IN ('no_answer', 'declined', 'busy'));
ALTER TABLE calls DROP COLUMN IF EXISTS join_requested_at;
DROP TABLE IF EXISTS standing_rings;
