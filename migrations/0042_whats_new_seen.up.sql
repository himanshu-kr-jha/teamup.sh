-- The newest "What's new" entry somebody has seen, by its id (a date such as
-- 2026-11-12), so the page opens by itself once after each update and not
-- again, on whichever device they use. Empty until they have seen one.
ALTER TABLE users ADD COLUMN IF NOT EXISTS whats_new_seen VARCHAR(40);
