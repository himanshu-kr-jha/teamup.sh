-- A missed call belongs in the conversation, so you find out you were called
-- by looking where you would look anyway. It is not something somebody wrote,
-- though, so it is marked as an event rather than a message and rendered
-- differently.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS kind VARCHAR(20) NOT NULL DEFAULT 'user';

-- Whether a call ever connected, which is what separates a missed call from a
-- short one. Derived today by counting participants who joined; recorded here
-- so the record still says so once the participant rows are gone.
ALTER TABLE calls ADD COLUMN IF NOT EXISTS answered BOOLEAN NOT NULL DEFAULT FALSE;
