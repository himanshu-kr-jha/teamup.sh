-- Full-text search only matches whole words, so "deploment" or a partial name
-- finds nothing at all. Trigram similarity fills that in: it compares three
-- letter runs, which tolerates a typo and matches inside a word.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- GIN over trigrams is what makes ILIKE and similarity() usable at size;
-- without it both degrade to a scan of every message.
CREATE INDEX IF NOT EXISTS idx_messages_content_trgm
    ON messages USING GIN (content gin_trgm_ops);

-- Files are searched by name, which is short enough that trigrams alone do.
CREATE INDEX IF NOT EXISTS idx_attachments_filename_trgm
    ON attachments USING GIN (filename gin_trgm_ops);
