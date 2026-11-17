-- Back to every team holding its name, deleted or not. Deleted teams that share
-- a name with another team are renamed first, so the constraint can return.
DROP INDEX teams_org_id_name_live;
UPDATE teams t SET name = t.name || ' (deleted ' || to_char(t.deleted_at, 'YYYY-MM-DD HH24:MI:SS') || ')'
WHERE t.deleted_at IS NOT NULL
  AND EXISTS (SELECT 1 FROM teams o WHERE o.org_id = t.org_id AND o.name = t.name AND o.id <> t.id);
ALTER TABLE teams ADD CONSTRAINT teams_org_id_name_key UNIQUE (org_id, name);
