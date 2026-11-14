-- Each nightly backup says here how it went (scripts/backup-db.sh), so the
-- System tab can show when the last one ran and whether it reached the files
-- bucket, and warn when one is missed. Nothing else reads or writes it.
CREATE TABLE IF NOT EXISTS backup_runs (
    id          BIGSERIAL PRIMARY KEY,
    finished_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    size_bytes  BIGINT NOT NULL,
    -- NULL when there is no outside bucket and the backup stays on the server.
    uploaded    BOOLEAN,
    problem     TEXT
);
CREATE INDEX IF NOT EXISTS idx_backup_runs_finished ON backup_runs (finished_at DESC);
