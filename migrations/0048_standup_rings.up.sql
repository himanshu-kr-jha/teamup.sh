-- A stand-up rings everybody it is for when it starts, once per day: this is the
-- claim that keeps several replicas from ringing twice.
CREATE TABLE IF NOT EXISTS standing_rings (
    standing_meeting_id UUID NOT NULL REFERENCES standing_meetings(id) ON DELETE CASCADE,
    occurrence_date     DATE NOT NULL,
    rung_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (standing_meeting_id, occurrence_date)
);

-- The ring opens the day's room before anybody asks to join. Attendance reads a
-- day nobody connected to as unknown only when somebody did ask, which is what
-- lost webhooks look like; a room only the ring opened is everybody absent.
ALTER TABLE calls ADD COLUMN IF NOT EXISTS join_requested_at TIMESTAMPTZ;
UPDATE calls SET join_requested_at = COALESCE(started_at, created_at)
WHERE standing_meeting_id IS NOT NULL AND join_requested_at IS NULL;

-- A call to somebody on leave is not rung, and is marked as such.
ALTER TABLE calls DROP CONSTRAINT IF EXISTS calls_missed_reason_check;
ALTER TABLE calls ADD CONSTRAINT calls_missed_reason_check
    CHECK (missed_reason IN ('no_answer', 'declined', 'busy', 'on_leave'));
