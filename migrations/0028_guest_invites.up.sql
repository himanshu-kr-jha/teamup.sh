-- A second address for a meeting's guests, that lets them in without the
-- passcode; the passcode link stays for when that one cannot be used.
ALTER TABLE call_guest_links ADD COLUMN IF NOT EXISTS direct_token TEXT UNIQUE;

-- A guest who has joined once can come back from the same browser without the
-- passcode: the browser keeps a key, and this is its SHA-256.
ALTER TABLE call_guests ADD COLUMN IF NOT EXISTS rejoin_hash TEXT;
CREATE INDEX IF NOT EXISTS idx_call_guests_rejoin ON call_guests(call_id, rejoin_hash);

-- People from outside invited to a meeting by email, with the time zone of
-- whoever invited them, which their emails give the time in.
CREATE TABLE IF NOT EXISTS call_guest_invites (
    call_id    UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    time_zone  TEXT NOT NULL DEFAULT 'UTC',
    invited_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    emailed_at TIMESTAMPTZ,
    PRIMARY KEY (call_id, email)
);
