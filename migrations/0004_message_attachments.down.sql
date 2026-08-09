DROP INDEX IF EXISTS idx_attachments_message;
ALTER TABLE attachments DROP COLUMN IF EXISTS message_id;
