-- A deleted member keeps their row, because other rows still point at it, but
-- they should not keep their address: an administrator has to be able to add
-- the same person back.
ALTER TABLE users DROP CONSTRAINT users_org_id_email_key;
CREATE UNIQUE INDEX users_org_id_email_key ON users (org_id, email) WHERE deleted_at IS NULL;
