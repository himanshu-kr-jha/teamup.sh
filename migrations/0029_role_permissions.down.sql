-- Back to the old names. Nothing checked them, so nobody gains or loses access;
-- a role made since keeps whatever of its permissions had an old name.
UPDATE roles SET permissions = '["org:manage","users:manage","roles:manage","channels:manage","tasks:manage","calls:manage","settings:manage"]'::jsonb,
    updated_at = NOW() WHERE is_system AND name = 'admin';
UPDATE roles SET permissions = '["channels:read","channels:write","tasks:manage","calls:manage","reports:read"]'::jsonb,
    updated_at = NOW() WHERE is_system AND name = 'manager';
UPDATE roles SET permissions = '["channels:read","channels:write","tasks:read","tasks:write:self","calls:join"]'::jsonb,
    updated_at = NOW() WHERE is_system AND name = 'member';

UPDATE roles r SET permissions = COALESCE((
    SELECT jsonb_agg(DISTINCT n) FROM jsonb_array_elements_text(r.permissions) AS o(new)
    CROSS JOIN LATERAL (SELECT unnest(CASE o.new
        WHEN 'org.settings'   THEN ARRAY['org:manage']
        WHEN 'notes.settings' THEN ARRAY['settings:manage']
        WHEN 'members.manage' THEN ARRAY['users:manage']
        WHEN 'roles.manage'   THEN ARRAY['roles:manage']
        WHEN 'channels.all'   THEN ARRAY['channels:manage']
        WHEN 'tasks.all'      THEN ARRAY['tasks:manage']
        WHEN 'meetings.all'   THEN ARRAY['calls:manage']
        ELSE ARRAY[]::text[] END)) AS t(n)), '[]'::jsonb),
    updated_at = NOW()
WHERE NOT r.is_system;
