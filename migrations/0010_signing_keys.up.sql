-- The key that signs access tokens used to be generated in-process at every
-- start and never written down, so two replicas rejected each other's tokens
-- and a restart signed everybody out. It belongs with the rest of the
-- deployment's state: one row, shared by every replica, created once by
-- whichever process gets there first.
--
-- wrapping records how private_key is stored: 'none' for a PKCS#8 DER key, or
-- 'aes-256-gcm' for one encrypted with a key derived from TEAMUP_AUTH_SECRET
-- (nonce first, then the sealed key), so a database dump on its own does not
-- hand over the ability to mint tokens.
CREATE TABLE IF NOT EXISTS signing_keys (
    kid             UUID PRIMARY KEY,
    algorithm       VARCHAR(20) NOT NULL,
    wrapping        VARCHAR(20) NOT NULL,
    private_key     BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_signing_keys_created ON signing_keys(created_at DESC);
