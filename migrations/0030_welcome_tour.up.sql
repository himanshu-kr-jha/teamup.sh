-- When somebody finished, or skipped, the tour of TeamUp they are shown the first
-- time they sign in. Empty until then, so everybody, new or already here, sees it
-- once; set back to empty to show it again.
ALTER TABLE users ADD COLUMN tour_completed_at TIMESTAMPTZ;
