-- Channel history could not use an index, and so scanned the whole table.
--
-- The only index over (channel_id, created_at) was partial:
--
--   CREATE INDEX idx_messages_channel_time ON messages(channel_id, created_at DESC)
--       WHERE deleted_at IS NULL;
--
-- and the history query deliberately does not filter deleted_at, because a
-- deleted message has to keep its place: replies point at it, and a visible gap
-- is better than history silently closing over one. A partial index can only
-- serve a query the planner can prove never needs the excluded rows, so the
-- predicate that makes history correct is exactly the one that made the index
-- unusable. Measured at 100k messages: a parallel sequential scan, ~65 ms per
-- channel open — and this query runs on every channel open, every socket
-- reconnect and every tab focus.
--
-- The fix is the index, not the query. Tombstones are around 2% of a busy
-- table, so the partial index bought a rounding error of size in exchange for
-- being unusable by the one query that matters most. A plain index serves both
-- shapes: the history query that wants tombstones, and the unread counts and
-- listings that filter them out (which recheck deleted_at on the heap, for 2%
-- of rows).
--
-- Built under a lock rather than CONCURRENTLY, because golang-migrate runs each
-- migration in a transaction and CREATE INDEX CONCURRENTLY cannot run in one.
-- On a table of this size that is a pause, not an outage; a deployment with
-- millions of messages should build it concurrently by hand first, at which
-- point the IF NOT EXISTS below makes this migration a no-op.
CREATE INDEX IF NOT EXISTS idx_messages_channel_created
    ON messages (channel_id, created_at DESC);

DROP INDEX IF EXISTS idx_messages_channel_time;
