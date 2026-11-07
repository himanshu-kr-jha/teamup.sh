-- Being added to a task by somebody else counts as Added to; adding yourself is only
-- following it (decision 38). NULL means somebody else, not known who: the account
-- that did it has gone, or the row predates this column.
ALTER TABLE task_watchers
    ADD COLUMN IF NOT EXISTS added_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- Rows from before now do not say who added them, but being added by somebody else
-- has always raised a task.added notification, and adding yourself never has, so that
-- is the evidence. With none, it counts as having added yourself: taking credit away
-- from somebody is the smaller error than counting a task they only chose to follow.
UPDATE task_watchers w
SET added_by = w.user_id
WHERE w.added_by IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM notifications n
      WHERE n.user_id = w.user_id AND n.type = 'task.added' AND n.data->>'taskId' = w.task_id::text
  );
