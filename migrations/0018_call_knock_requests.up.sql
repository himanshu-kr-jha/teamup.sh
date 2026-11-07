-- Somebody who is not a member of a private call asking to come in.
CREATE TABLE IF NOT EXISTS call_knock_requests (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    call_id     UUID NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status      VARCHAR(20) NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'admitted', 'denied', 'ended')),
    answered_by UUID REFERENCES users(id) ON DELETE SET NULL,
    answered_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- A double click is one request.
CREATE UNIQUE INDEX IF NOT EXISTS idx_call_knock_one_pending
    ON call_knock_requests (call_id, user_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_call_knock_call ON call_knock_requests (call_id, created_at DESC);
