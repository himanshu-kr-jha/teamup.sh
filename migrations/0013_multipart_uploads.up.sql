-- A resumable upload has to survive the browser that started it. The point of
-- multipart is that a 2 GB screen recording which dies at 80% resumes rather
-- than restarts, and after a page reload the tab remembers nothing.
--
-- This table holds the upload's IDENTITY and its PLAN, and nothing about
-- progress. Which parts actually landed is asked of object storage
-- (ListObjectParts) every time, because a row here can drift from reality —
-- a part can land and its acknowledgement be lost — whereas the store cannot
-- disagree with itself.
--
-- The plan (total_bytes, part_size, part_count) is what makes the size limit
-- enforceable. Each part URL is signed for one exact part number and one exact
-- content length, so the number of parts and the bytes in each are both fixed
-- here at initiate time and cannot be renegotiated by the client afterwards.
CREATE TABLE IF NOT EXISTS multipart_uploads (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category          VARCHAR(20) NOT NULL,
    ref_id            VARCHAR(255) NOT NULL,
    object_key        VARCHAR(1024) NOT NULL,
    -- The opaque id object storage gave us. Not unique on its own: it is only
    -- meaningful together with the object key.
    storage_upload_id TEXT NOT NULL,
    filename          VARCHAR(512) NOT NULL,
    content_type      VARCHAR(128) NOT NULL DEFAULT '',
    total_bytes       BIGINT NOT NULL,
    part_size         BIGINT NOT NULL,
    part_count        INTEGER NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at      TIMESTAMPTZ,
    UNIQUE (object_key, storage_upload_id)
);

-- Offering somebody their interrupted uploads back is a lookup by parent.
CREATE INDEX IF NOT EXISTS idx_multipart_resumable
    ON multipart_uploads (org_id, created_by, category, ref_id)
    WHERE completed_at IS NULL;

-- The hourly sweep walks the oldest unfinished uploads.
CREATE INDEX IF NOT EXISTS idx_multipart_abandoned
    ON multipart_uploads (created_at)
    WHERE completed_at IS NULL;
