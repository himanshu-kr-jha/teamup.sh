-- What a blocked task is waiting for: a short note, and optionally the person
-- and the task it waits on. Only a blocked task has one; it is cleared when the
-- task moves on, and the task's activity keeps what it said.
--
-- A task blocked before this has a NULL note, which reads as "No reason given",
-- the same as an empty one.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS blocked_note TEXT;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS blocked_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS blocked_by_task_id UUID REFERENCES tasks(id) ON DELETE SET NULL;

-- A saved scrum keeps what each blocked task was blocked on as it was then, as
-- text, like the rest of the snapshot. NULL on everything else and on scrums
-- saved before this.
ALTER TABLE scrum_entries ADD COLUMN IF NOT EXISTS blocked_note TEXT;
ALTER TABLE scrum_entries ADD COLUMN IF NOT EXISTS blocked_by_name TEXT;
ALTER TABLE scrum_entries ADD COLUMN IF NOT EXISTS blocked_by_task_title TEXT;
