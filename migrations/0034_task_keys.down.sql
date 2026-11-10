DELETE FROM task_activities WHERE user_id IS NULL;
ALTER TABLE task_activities DROP COLUMN IF EXISTS source;
ALTER TABLE task_activities ALTER COLUMN user_id SET NOT NULL;
DROP INDEX IF EXISTS idx_tasks_key;
ALTER TABLE tasks DROP COLUMN IF EXISTS task_key;
DROP TABLE IF EXISTS task_key_counters;
