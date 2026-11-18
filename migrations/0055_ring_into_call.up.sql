-- Somebody in a call can ring a colleague into it (the owner's request of
-- 18 November 2026). Their row in call_participants says who rang them and
-- when, and when that ring stopped: answered, turned down, rung out or
-- cancelled. A ring that has not stopped is still ringing.
ALTER TABLE call_participants
    ADD COLUMN IF NOT EXISTS rung_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS rung_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS ring_ended_at TIMESTAMPTZ;

-- Who rang whom is on the call's trail, which is also how ringing the same
-- person over and over is limited.
ALTER TABLE call_events DROP CONSTRAINT IF EXISTS call_events_type_check;
ALTER TABLE call_events ADD CONSTRAINT call_events_type_check CHECK (type IN (
    'knocked', 'admitted', 'denied', 'joined', 'left',
    'screen_share_started', 'screen_share_stopped', 'camera_on', 'rung'));
