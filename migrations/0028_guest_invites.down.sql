-- Forgets who was invited by email, the direct links and the rejoin keys; the
-- passcode links and the record of guests stay.
DROP TABLE IF EXISTS call_guest_invites;
DROP INDEX IF EXISTS idx_call_guests_rejoin;
ALTER TABLE call_guests DROP COLUMN IF EXISTS rejoin_hash;
ALTER TABLE call_guest_links DROP COLUMN IF EXISTS direct_token;
