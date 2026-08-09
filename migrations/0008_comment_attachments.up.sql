-- A file attached to a comment belongs to that comment, not merely to the task,
-- so it can be shown with the words it came with. ref_id still holds the task
-- and still governs access: a comment id here never widens who can read it.
ALTER TABLE attachments ADD COLUMN IF NOT EXISTS comment_id UUID
    REFERENCES task_comments(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_attachments_comment ON attachments(comment_id)
    WHERE comment_id IS NOT NULL;
