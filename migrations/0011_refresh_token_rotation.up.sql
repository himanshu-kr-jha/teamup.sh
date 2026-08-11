-- Rotation revoked the presented refresh token before issuing its
-- replacement, so of ten requests arriving together — a PWA refetching its
-- queries on focus, or every client refreshing after a restart — one succeeded
-- and nine were signed out. Recording which token replaced which lets a caller
-- that lost the race be handed an access token on the new session instead of a
-- 401, for a short window after the rotation.
ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS replaced_by UUID REFERENCES refresh_tokens(id) ON DELETE SET NULL;
