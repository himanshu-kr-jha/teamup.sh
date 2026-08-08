-- Public and announcement channels belong to the whole organization, but until
-- now only their creator was ever a member. Everybody else could see the
-- channel and was refused on every write, and announcements reached nobody.
-- Backfill the membership rows the new model assumes.
INSERT INTO channel_members (channel_id, user_id, role, joined_at)
SELECT c.id, u.id, 'member', NOW()
FROM channels c
JOIN users u ON u.org_id = c.org_id
WHERE c.type IN ('public', 'announcement')
  AND c.deleted_at IS NULL
  AND COALESCE(c.is_archived, false) = false
  AND u.deleted_at IS NULL
ON CONFLICT (channel_id, user_id) DO NOTHING;

-- Announcement channels were created private, which kept them out of the
-- channel list for everybody but their creator.
UPDATE channels SET is_private = FALSE
WHERE type IN ('public', 'announcement') AND deleted_at IS NULL;
