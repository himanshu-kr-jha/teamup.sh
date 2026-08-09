DROP INDEX IF EXISTS idx_attachments_filename_trgm;
DROP INDEX IF EXISTS idx_messages_content_trgm;
-- The extension is left in place: other things may have come to rely on it.
