-- Access somebody already held on GitHub or Bitbucket before TeamUp looked,
-- recorded so they are not asked to request what they have.
ALTER TABLE project_access_grants ADD COLUMN IF NOT EXISTS source VARCHAR(10) NOT NULL DEFAULT 'teamup';
ALTER TABLE project_access_grants DROP CONSTRAINT IF EXISTS project_access_grants_source_check;
ALTER TABLE project_access_grants ADD CONSTRAINT project_access_grants_source_check CHECK (source IN ('teamup', 'found'));
