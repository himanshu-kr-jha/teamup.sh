-- Why a call between two people never happened, for the calls list to say so
-- with the right mark: 'no_answer', 'declined', or 'busy' (the person called
-- was already in another call or meeting). Empty for a call that happened.
ALTER TABLE calls ADD COLUMN IF NOT EXISTS missed_reason VARCHAR(20)
    CHECK (missed_reason IN ('no_answer', 'declined', 'busy'));

-- What the conversations already say about calls that ended before now.
UPDATE calls c SET missed_reason = CASE WHEN m.content = 'Call declined' THEN 'declined' ELSE 'no_answer' END
FROM (
    SELECT DISTINCT ON (c2.id) c2.id, msg.content
    FROM calls c2
    JOIN messages msg ON msg.kind = 'event' AND msg.user_id = c2.created_by
     AND msg.created_at BETWEEN COALESCE(c2.ended_at, c2.created_at) - INTERVAL '5 seconds'
                            AND COALESCE(c2.ended_at, c2.created_at) + INTERVAL '2 minutes'
    WHERE c2.answered = false AND c2.scheduled_start IS NULL AND c2.status = 'ended'
    ORDER BY c2.id, msg.created_at
) m
WHERE c.id = m.id AND c.missed_reason IS NULL;
