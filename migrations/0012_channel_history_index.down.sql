-- Back to the partial index, and back to a sequential scan on every channel
-- open. See the up migration for why that follows.
CREATE INDEX IF NOT EXISTS idx_messages_channel_time
    ON messages (channel_id, created_at DESC) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_messages_channel_created;
