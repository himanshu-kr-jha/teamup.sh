-- Attachments were filed against the channel only, so an upload appeared in a
-- channel-wide file strip the moment it finished rather than being sent as part
-- of a message. Binding one to a message lets it travel with the text.
--
-- ref_id still holds the channel and still governs access: entitlement follows
-- the channel, so a message id here never widens who can read the file.
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS message_id UUID REFERENCES messages(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_attachments_message ON attachments(message_id) WHERE message_id IS NOT NULL;
