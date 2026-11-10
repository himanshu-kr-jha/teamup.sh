-- Bitbucket knows a person by two ids: their uuid, and the Atlassian account id
-- its @-mentions are written with. Both are kept, to know them by either.
ALTER TABLE code_identities ADD COLUMN IF NOT EXISTS account_id VARCHAR(128) NOT NULL DEFAULT '';
