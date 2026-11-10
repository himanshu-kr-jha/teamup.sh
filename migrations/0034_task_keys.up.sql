-- Every task has a key, such as PHO-12: its project's key and a number, or
-- TASK and a number for a task in no project. A branch, commit or pull
-- request that names the key is linked to the task. The key never changes,
-- even if the task moves to another project, so links stay right.
CREATE TABLE IF NOT EXISTS task_key_counters (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    prefix      VARCHAR(10) NOT NULL,
    last_number INT NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, prefix)
);

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS task_key VARCHAR(24);

-- The tasks there are already, numbered in the order they were made.
WITH numbered AS (
    SELECT t.id, t.org_id, COALESCE(p.key, 'TASK') AS prefix,
           ROW_NUMBER() OVER (PARTITION BY t.org_id, COALESCE(p.key, 'TASK') ORDER BY t.created_at, t.id) AS n
    FROM tasks t LEFT JOIN projects p ON p.id = t.project_id
    WHERE t.task_key IS NULL
)
UPDATE tasks SET task_key = numbered.prefix || '-' || numbered.n FROM numbered WHERE tasks.id = numbered.id;

INSERT INTO task_key_counters (org_id, prefix, last_number)
SELECT org_id, split_part(task_key, '-', 1), MAX(split_part(task_key, '-', 2)::int)
FROM tasks WHERE task_key IS NOT NULL
GROUP BY org_id, split_part(task_key, '-', 1)
ON CONFLICT (org_id, prefix) DO UPDATE SET last_number = GREATEST(task_key_counters.last_number, EXCLUDED.last_number);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_key ON tasks (org_id, task_key);

-- A change made by GitHub or Bitbucket, not by a person, is recorded as theirs.
ALTER TABLE task_activities ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE task_activities ADD COLUMN IF NOT EXISTS source VARCHAR(20);
