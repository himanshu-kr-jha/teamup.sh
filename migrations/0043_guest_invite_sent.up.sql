-- When each guest invitation was last emailed, so asking again sends it again
-- only once the last one is a while old, and nobody can use the invitations to
-- send the organization's mail to the same people over and over.
ALTER TABLE call_guest_invites ADD COLUMN IF NOT EXISTS last_emailed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_call_guest_invites_inviter_sent ON call_guest_invites (invited_by, last_emailed_at);
