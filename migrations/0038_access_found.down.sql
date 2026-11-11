DELETE FROM project_access_grants WHERE source = 'found';
ALTER TABLE project_access_grants DROP CONSTRAINT IF EXISTS project_access_grants_source_check;
ALTER TABLE project_access_grants DROP COLUMN IF EXISTS source;
