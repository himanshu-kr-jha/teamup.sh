-- What somebody does at the company, such as "AI/ML Engineer", which they set
-- themselves and everyone in the organization sees (the owner's request of
-- 17 November 2026). Not the same as users.role, which is about permissions.
ALTER TABLE users ADD COLUMN job_title VARCHAR(80) NOT NULL DEFAULT '';
