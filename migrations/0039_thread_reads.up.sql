-- How far each person has read each thread. A reply is not in the
-- conversation's timeline, so the conversation's read marker says nothing
-- about it: it was only ever crossed by reading something posted later in the
-- conversation, and opening the thread moved nothing, so a reply kept one tick
-- after it had been read.
CREATE TABLE IF NOT EXISTS thread_reads (
    root_id      UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    last_read_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (root_id, user_id)
);

-- What the ticks said until now, so no reply that showed as read goes back to
-- one tick: each member had read a thread as far as their conversation marker.
INSERT INTO thread_reads (root_id, user_id, last_read_at)
SELECT DISTINCT r.reply_to, cm.user_id, cm.last_read_at
FROM messages r
JOIN channel_members cm ON cm.channel_id = r.channel_id
WHERE r.reply_to IS NOT NULL AND cm.last_read_at IS NOT NULL
ON CONFLICT DO NOTHING;
