DELETE FROM call_events WHERE type = 'rung';
ALTER TABLE call_events DROP CONSTRAINT IF EXISTS call_events_type_check;
ALTER TABLE call_events ADD CONSTRAINT call_events_type_check CHECK (type IN (
    'knocked', 'admitted', 'denied', 'joined', 'left',
    'screen_share_started', 'screen_share_stopped', 'camera_on'));
ALTER TABLE call_participants
    DROP COLUMN IF EXISTS rung_by,
    DROP COLUMN IF EXISTS rung_at,
    DROP COLUMN IF EXISTS ring_ended_at;
