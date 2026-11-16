-- Self-service password resets: somebody who forgot their password asks for a
-- link by email. Only a hash of the link's token is kept, so the table alone
-- cannot be used to take over an account.
CREATE TABLE password_resets (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    BYTEA NOT NULL UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at    TIMESTAMPTZ NOT NULL,
    -- Set when the link was used, or when a newer link or another password
    -- change made it worthless. A link works once at most.
    used_at       TIMESTAMPTZ,
    requested_ip  INET,
    requested_ua  TEXT
);

CREATE INDEX idx_password_resets_user ON password_resets(user_id, created_at DESC);
