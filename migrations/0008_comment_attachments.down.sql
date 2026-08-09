DROP INDEX IF EXISTS idx_attachments_comment;
ALTER TABLE attachments DROP COLUMN IF EXISTS comment_id;
