-- Only a live team holds its name: a deleted team's name may be used again
-- (found 17 November 2026, when a deleted team's name was refused).
ALTER TABLE teams DROP CONSTRAINT teams_org_id_name_key;
CREATE UNIQUE INDEX teams_org_id_name_live ON teams (org_id, name) WHERE deleted_at IS NULL;
