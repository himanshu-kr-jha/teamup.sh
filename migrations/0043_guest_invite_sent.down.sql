DROP INDEX IF EXISTS idx_call_guest_invites_inviter_sent;
ALTER TABLE call_guest_invites DROP COLUMN IF EXISTS last_emailed_at;
