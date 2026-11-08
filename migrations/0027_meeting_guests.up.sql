-- Guests from outside the organization, let into one meeting by a link.

-- A meeting's guest link: the token in its address, an optional passcode, and
-- whether it is on. The passcode is kept as typed, because the people in the
-- meeting are shown it to pass on, as they are shown the link itself.
CREATE TABLE IF NOT EXISTS call_guest_links (
    call_id    UUID PRIMARY KEY REFERENCES calls(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token      TEXT NOT NULL UNIQUE,
    passcode   TEXT,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Everybody who came in as a guest, by the name they gave: who was there, and
-- who was put out and may not come back in with the token they had.
CREATE TABLE IF NOT EXISTS call_guests (
    id         UUID PRIMARY KEY,
    call_id    UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    removed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_call_guests_call ON call_guests(call_id);

-- Wrong passcodes, to slow down guessing: by address, and for the link as a whole.
CREATE TABLE IF NOT EXISTS call_guest_attempts (
    call_id UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    ip      TEXT NOT NULL,
    at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_call_guest_attempts ON call_guest_attempts(call_id, at);
