-- The organization's own background pictures for calls (the owner's request of
-- 19 November 2026): an administrator puts in a set, and those are the only
-- pictures anybody can put behind their camera.
--
-- Kept in the database, as profile pictures and the company templates are,
-- rather than in object storage: they are read through TeamUp's own address,
-- which never expires and is the page's own origin, so a camera's canvas may
-- draw them. Each is brought to 16:9 when it is put in, at most 1920 x 1080,
-- with a small copy beside it for the menu.
CREATE TABLE IF NOT EXISTS org_call_pictures (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name         VARCHAR(60) NOT NULL DEFAULT '',
    position     INT NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL,
    data         BYTEA NOT NULL,
    thumb        BYTEA NOT NULL,
    width        INT NOT NULL,
    height       INT NOT NULL,
    version      TEXT NOT NULL,
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_org_call_pictures_org ON org_call_pictures (org_id, position, created_at);
