-- A message passed on from another conversation says so.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS forwarded BOOLEAN NOT NULL DEFAULT FALSE;

-- Profile pictures, already shrunk by the browser to a small square. Kept with
-- the person rather than in object storage, because they are tiny, read on every
-- page, and must keep working at a fixed address; a presigned link expires.
CREATE TABLE IF NOT EXISTS user_avatars (
    user_id      UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    content_type TEXT NOT NULL,
    data         BYTEA NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
