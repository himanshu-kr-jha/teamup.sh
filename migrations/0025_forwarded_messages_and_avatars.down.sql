-- Avatars set by upload pointed at the table that goes, so they are cleared.
UPDATE users SET avatar_url = NULL WHERE avatar_url LIKE '/api/users/%/avatar%';
DROP TABLE IF EXISTS user_avatars;
ALTER TABLE messages DROP COLUMN IF EXISTS forwarded;
