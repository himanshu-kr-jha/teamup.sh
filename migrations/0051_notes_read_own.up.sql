-- Meeting notes are only for whoever is given them (the owner's decision of 16
-- November). Reading the notes of meetings you were in is now a permission,
-- notes.read_own; administrators have it, and Read every meeting's minutes
-- covers it. Nobody else keeps reading notes by having been in the meeting.
UPDATE roles SET permissions = permissions || '["notes.read_own"]'::jsonb, updated_at = NOW()
WHERE is_system AND name = 'admin' AND NOT permissions ? 'notes.read_own';
