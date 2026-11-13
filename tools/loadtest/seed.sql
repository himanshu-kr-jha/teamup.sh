-- Seeds a TEST server with users and conversations for tools/loadtest.
-- Never run it against a server with real people on it.
--
--   psql -v n=600 -v domain=loadtest.example -v owner=owner@loadtest.example -f seed.sql
--
-- Makes load1..loadN@domain, members with the owner's password, so one test
-- password signs in all of them; a "general" conversation with everybody; and
-- one conversation per team of 25, as a company of teams has. Each also gets
-- 200 old messages, so reading a conversation reads a real page of history.

\set ON_ERROR_STOP on
BEGIN;

CREATE TEMP TABLE seed AS
SELECT o.org_id, o.id AS owner_id, o.password_hash,
       (SELECT id FROM roles r WHERE r.org_id = o.org_id AND r.name = 'member') AS role_id
FROM users o WHERE o.email = :'owner';

INSERT INTO users (id, org_id, name, email, password_hash, role, is_admin, must_change_password, created_at, updated_at)
SELECT gen_random_uuid(), s.org_id, 'Load User ' || g, 'load' || g || '@' || :'domain', s.password_hash, 'member', FALSE, FALSE, NOW(), NOW()
FROM seed s, generate_series(1, :n) g
ON CONFLICT DO NOTHING;

INSERT INTO user_roles (user_id, role_id, assigned_at)
SELECT u.id, s.role_id, NOW() FROM users u, seed s
WHERE u.email LIKE 'load%@' || :'domain'
ON CONFLICT DO NOTHING;

-- Conversations: general, and team-1 .. team-K of 25 people each.
INSERT INTO channels (id, org_id, name, type, description, created_by, is_private, created_at, updated_at)
SELECT gen_random_uuid(), s.org_id, 'general', 'public', 'everybody', s.owner_id, FALSE, NOW(), NOW() FROM seed s;
INSERT INTO channels (id, org_id, name, type, description, created_by, is_private, created_at, updated_at)
SELECT gen_random_uuid(), s.org_id, 'team-' || k, 'group', 'a team', s.owner_id, TRUE, NOW(), NOW()
FROM seed s, generate_series(1, (:n + 24) / 25) k;

INSERT INTO channel_members (channel_id, user_id, role, joined_at)
SELECT c.id, u.id, 'member', NOW()
FROM channels c, users u
WHERE c.name = 'general' AND u.email LIKE 'load%@' || :'domain'
ON CONFLICT DO NOTHING;

INSERT INTO channel_members (channel_id, user_id, role, joined_at)
SELECT c.id, u.id, 'member', NOW()
FROM users u
JOIN channels c ON c.name = 'team-' || ((substring(u.email FROM 'load([0-9]+)@')::int - 1) / 25 + 1)
WHERE u.email LIKE 'load%@' || :'domain'
ON CONFLICT DO NOTHING;

-- History: 200 messages in each conversation, from its members.
INSERT INTO messages (id, org_id, channel_id, user_id, content, created_at)
SELECT gen_random_uuid(), c.org_id, c.id, m.user_id,
       'Earlier message ' || g || ' about the deploy, the review and the meeting',
       NOW() - (g || ' minutes')::interval
FROM channels c
JOIN LATERAL (SELECT user_id FROM channel_members cm WHERE cm.channel_id = c.id LIMIT 1) m ON TRUE
CROSS JOIN generate_series(1, 200) g
WHERE c.name = 'general' OR c.name LIKE 'team-%';

COMMIT;
ANALYZE;
SELECT count(*) AS users FROM users;
SELECT count(*) AS channels FROM channels;
SELECT count(*) AS messages FROM messages;
