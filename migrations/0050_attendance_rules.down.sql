UPDATE roles SET permissions = permissions - 'attendance.review' - 'attendance.view';
DROP TABLE IF EXISTS attendance_reviews;
DROP TABLE IF EXISTS field_checkins;
ALTER TABLE calls DROP COLUMN IF EXISTS camera_required, DROP COLUMN IF EXISTS attend_before_min, DROP COLUMN IF EXISTS attend_after_min;
ALTER TABLE standing_meetings DROP COLUMN IF EXISTS attend_before_min, DROP COLUMN IF EXISTS attend_after_min;
DELETE FROM call_events WHERE type = 'camera_on';
ALTER TABLE call_events DROP CONSTRAINT IF EXISTS call_events_type_check;
ALTER TABLE call_events ADD CONSTRAINT call_events_type_check CHECK (type IN (
    'knocked', 'admitted', 'denied', 'joined', 'left',
    'screen_share_started', 'screen_share_stopped'));
ALTER TABLE standing_meetings DROP COLUMN IF EXISTS camera_required;
