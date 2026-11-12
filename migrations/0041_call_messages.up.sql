-- What people write to each other in a meeting's own chat, as in a video
-- meeting's side panel: everybody in the room, guests included, reads it while
-- the meeting is on, and it stays with the call afterwards for whoever may read
-- the call's log. A message is from a member (sender_user_id) or a guest
-- (sender_guest_id), never both; the name is kept as it was shown.
CREATE TABLE IF NOT EXISTS call_messages (
    id              UUID PRIMARY KEY,
    call_id         UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    sender_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    sender_guest_id UUID REFERENCES call_guests(id) ON DELETE SET NULL,
    sender_name     VARCHAR(120) NOT NULL,
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (sender_user_id IS NULL OR sender_guest_id IS NULL)
);

CREATE INDEX IF NOT EXISTS idx_call_messages_call_time ON call_messages(call_id, created_at, id);
