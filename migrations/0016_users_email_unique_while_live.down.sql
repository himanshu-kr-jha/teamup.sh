DROP INDEX users_org_id_email_key;

-- Somebody who left and came back now has two rows with one address. The old
-- constraint cannot hold that, so the deleted rows give the address up.
UPDATE users d
SET email = LEFT(d.email, 200) || '.deleted.' || d.id
WHERE d.deleted_at IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM users o
    WHERE o.org_id = d.org_id AND o.email = d.email AND o.id <> d.id
  );

ALTER TABLE users ADD CONSTRAINT users_org_id_email_key UNIQUE (org_id, email);
