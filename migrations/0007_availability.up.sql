-- What somebody has chosen to say about themselves, as opposed to whether they
-- happen to be connected. Kept on the user rather than in Redis: "on leave"
-- has to outlive a reconnect, a restart, and a cache flush, and has to be
-- visible while they are not here at all — which is the whole point of it.
ALTER TABLE users ADD COLUMN IF NOT EXISTS availability VARCHAR(20) NOT NULL DEFAULT 'available';
ALTER TABLE users ADD COLUMN IF NOT EXISTS availability_note TEXT;
-- When they are expected back. Optional, and only meaningful for a status that
-- has an end, like leave.
ALTER TABLE users ADD COLUMN IF NOT EXISTS availability_until TIMESTAMPTZ;
