-- Without the column these rows would read as the person's own assigned work.
DELETE FROM scrum_entries WHERE added_to;
ALTER TABLE scrum_entries DROP COLUMN IF EXISTS added_to;
ALTER TABLE scrum_participants DROP COLUMN IF EXISTS added_to;
