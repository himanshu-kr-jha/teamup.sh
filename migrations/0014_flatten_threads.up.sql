-- Threads are one level deep, and the rows written before they existed are not.
--
-- reply_to has been stored on every message since the first migration and
-- nothing ever read it: there was no endpoint to fetch a thread and no UI to
-- show one, so a reply was recorded and then rendered inline like anything
-- else. Nothing therefore constrained what it pointed at, and two shapes got
-- in that the reader added alongside this migration cannot make sense of:
--
--   1. Chains. A reply to a reply to a reply. The new reader takes reply_to to
--      BE the thread root — that is what makes reading a thread one indexed
--      lookup instead of a recursive walk on every channel open. A chained
--      reply satisfies neither half of it: it is off the channel timeline for
--      having a parent, and out of its root's thread for not pointing at the
--      root. It would simply stop being visible anywhere.
--
--   2. Replies whose parent is in another channel. Nothing checked, so nothing
--      stopped it. Such a reply belongs to a conversation its own channel
--      cannot see, and it would drag a message into a thread belonging to a
--      channel its author may not even be in.
--
-- Both are fixed here rather than tolerated in the reader, because a reader
-- that copes with them has to keep coping with them for ever, and every new
-- query over messages has to remember to.
--
-- Deliberately not adding an index. The obvious candidates were measured
-- against 100k messages, 30% of them replies:
--
--   * counting replies for a 50-message page (reply_to = ANY(...) GROUP BY)
--     already uses idx_messages_reply: 0.5 ms, 107 buffers. A composite over
--     (reply_to, created_at) would save four heap blocks.
--   * the channel timeline, now filtering reply_to IS NULL, removes about one
--     row per fifty returned when replies are interleaved normally: 0.4 ms.
--     A partial index over (channel_id, created_at) WHERE reply_to IS NULL
--     would earn its write cost only in a channel whose recent history is
--     almost all thread replies, and that resolves itself the moment anybody
--     posts at top level.
--
-- An index nobody needs is not free: it is paid for on every message insert,
-- for ever. It can be added later from evidence.

-- A reply pointing into another channel was never a coherent thread. Cutting
-- it loose makes it a top-level message where it was actually written, which
-- is the only place its readers can see it — and the only place it was ever
-- safe for it to appear.
--
-- This runs first so that the chain walk below never has to follow an edge
-- across a channel boundary: every edge it can still see stays in one channel,
-- and so does every root it can reach.
UPDATE messages m
SET reply_to = NULL
FROM messages p
WHERE p.id = m.reply_to
  AND p.channel_id <> m.channel_id;

-- Then flatten what is left. Each reply is walked up to the first ancestor
-- that is not itself a reply, and re-pointed there.
--
-- The depth cap is a guard, not a limit: nothing in the application can build
-- a cycle (a reply can only name a message that already exists, and reply_to
-- is never updated afterwards), but a recursive CTE that meets one does not
-- terminate, and a migration that hangs is worse than one that leaves a row
-- alone. A message whose ancestry cannot be resolved inside the cap produces
-- no row below and is left exactly as it was.
WITH RECURSIVE ancestry (id, parent, depth) AS (
    SELECT m.id, m.reply_to, 1
    FROM messages m
    WHERE m.reply_to IS NOT NULL
  UNION ALL
    SELECT a.id, p.reply_to, a.depth + 1
    FROM ancestry a
    JOIN messages p ON p.id = a.parent
    WHERE p.reply_to IS NOT NULL
      AND p.reply_to <> a.id
      AND a.depth < 64
),
roots AS (
    SELECT DISTINCT ON (a.id) a.id, a.parent AS root
    FROM ancestry a
    JOIN messages p ON p.id = a.parent
    WHERE p.reply_to IS NULL
    ORDER BY a.id, a.depth
)
UPDATE messages m
SET reply_to = r.root
FROM roots r
WHERE m.id = r.id
  AND m.reply_to IS DISTINCT FROM r.root;
