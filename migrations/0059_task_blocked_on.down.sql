ALTER TABLE scrum_entries DROP COLUMN IF EXISTS blocked_by_task_title;
ALTER TABLE scrum_entries DROP COLUMN IF EXISTS blocked_by_name;
ALTER TABLE scrum_entries DROP COLUMN IF EXISTS blocked_note;
ALTER TABLE tasks DROP COLUMN IF EXISTS blocked_by_task_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS blocked_by_user_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS blocked_note;
