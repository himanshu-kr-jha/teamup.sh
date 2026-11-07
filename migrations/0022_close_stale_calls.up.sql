-- Calls whose last person closed a laptop stayed active for ever, until webhooks
-- recorded room_finished (decision 24), and those rows are still here. Nothing is
-- going to report on them now, so an unscheduled call still "active" a day after it
-- started is over, as of the last thing known about it. A meeting or an occurrence is
-- left to the periodic sweep, which can ask LiveKit.
WITH stale AS (
    SELECT c.id,
           COALESCE(
               (SELECT MAX(GREATEST(s.joined_at, s.left_at)) FROM call_sessions s WHERE s.call_id = c.id),
               (SELECT MAX(GREATEST(p.joined_at, p.left_at)) FROM call_participants p WHERE p.call_id = c.id),
               c.started_at,
               c.created_at
           ) AS over_at
    FROM calls c
    WHERE c.status = 'active'
      AND c.scheduled_start IS NULL
      AND COALESCE(c.started_at, c.created_at) < NOW() - interval '1 day'
), closed_calls AS (
    UPDATE calls c
    SET status = 'ended',
        ended_at = stale.over_at,
        duration_seconds = GREATEST(0, EXTRACT(EPOCH FROM (stale.over_at - COALESCE(c.started_at, c.created_at)))::int),
        updated_at = NOW()
    FROM stale
    WHERE c.id = stale.id
    RETURNING c.id
), closed_sessions AS (
    UPDATE call_sessions s
    SET left_at = stale.over_at
    FROM stale
    WHERE s.call_id = stale.id AND s.left_at IS NULL AND s.joined_at IS NOT NULL
    RETURNING s.call_id, s.user_id, s.participant_sid, s.left_at
), trail AS (
    INSERT INTO call_events (call_id, type, user_id, actor_id, at, dedupe_key)
    SELECT call_id, 'left', user_id, user_id, left_at, 'left:' || participant_sid FROM closed_sessions
    ON CONFLICT (call_id, dedupe_key) DO NOTHING
), closed_participants AS (
    UPDATE call_participants p
    SET left_at = stale.over_at
    FROM stale
    WHERE p.call_id = stale.id AND p.joined_at IS NOT NULL AND p.left_at IS NULL
)
UPDATE call_knock_requests r
SET status = 'ended', answered_at = stale.over_at
FROM stale
WHERE r.call_id = stale.id AND r.status = 'pending';

-- Every calendar read, availability read and conflict check filters on the person.
CREATE INDEX IF NOT EXISTS idx_call_participants_user ON call_participants (user_id, call_id);
