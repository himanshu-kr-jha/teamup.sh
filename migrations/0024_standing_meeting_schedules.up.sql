-- Absence is worked out on read, from the days a meeting ran. Reading that from the
-- meeting as it is now rewrote history whenever an admin edited its weekdays or time,
-- so each schedule is kept from the moment it took effect.
CREATE TABLE IF NOT EXISTS standing_meeting_schedules (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    standing_meeting_id UUID NOT NULL REFERENCES standing_meetings(id) ON DELETE CASCADE,
    effective_at        TIMESTAMPTZ NOT NULL,
    time_of_day         TEXT NOT NULL,
    time_zone           TEXT NOT NULL,
    weekdays            SMALLINT[] NOT NULL,
    duration_minutes    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_standing_meeting_schedules_meeting
    ON standing_meeting_schedules (standing_meeting_id, effective_at);

-- What a meeting's schedule was before now is not known, so it is taken to have
-- always been what it is.
INSERT INTO standing_meeting_schedules (standing_meeting_id, effective_at, time_of_day, time_zone, weekdays, duration_minutes)
SELECT m.id, m.created_at, m.time_of_day, m.time_zone, m.weekdays, m.duration_minutes
FROM standing_meetings m
WHERE NOT EXISTS (SELECT 1 FROM standing_meeting_schedules s WHERE s.standing_meeting_id = m.id);
