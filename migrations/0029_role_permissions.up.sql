-- Role permissions become what the server checks, from one list
-- (internal/auth/permissions.go). The built-in roles get that list's defaults,
-- and a role an organization made keeps what its old permissions meant.

UPDATE roles SET permissions = '["*"]'::jsonb, updated_at = NOW() WHERE is_system AND name = 'owner';
UPDATE roles SET permissions = '["org.settings","members.manage","roles.manage","teams.manage","tasks.all","channels.all","meetings.all","calls.record","notes.settings","notes.read_all"]'::jsonb,
    updated_at = NOW() WHERE is_system AND name = 'admin';
UPDATE roles SET permissions = '["calls.record"]'::jsonb, updated_at = NOW() WHERE is_system AND name = 'manager';
UPDATE roles SET permissions = '[]'::jsonb, updated_at = NOW() WHERE is_system AND name = 'member';

UPDATE roles r SET permissions = COALESCE((
    SELECT jsonb_agg(DISTINCT n) FROM jsonb_array_elements_text(r.permissions) AS o(old)
    CROSS JOIN LATERAL (SELECT unnest(CASE o.old
        WHEN 'org:manage'      THEN ARRAY['org.settings']
        WHEN 'settings:manage' THEN ARRAY['org.settings', 'notes.settings']
        WHEN 'users:manage'    THEN ARRAY['members.manage', 'teams.manage']
        WHEN 'roles:manage'    THEN ARRAY['roles.manage']
        WHEN 'channels:manage' THEN ARRAY['channels.all']
        WHEN 'tasks:manage'    THEN ARRAY['tasks.all']
        WHEN 'calls:manage'    THEN ARRAY['meetings.all', 'calls.record']
        ELSE ARRAY[]::text[] END)) AS t(n)), '[]'::jsonb),
    updated_at = NOW()
WHERE NOT r.is_system;
