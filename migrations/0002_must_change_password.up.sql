-- A member added by an administrator gets a temporary password, and has to
-- replace it before they can use anything. The flag rides in the access token
-- so enforcement costs no extra query per request.
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE;
