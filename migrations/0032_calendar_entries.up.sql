-- An entry is something somebody did or is doing, put on their own calendar:
-- work done in a meeting held outside TeamUp, say. To anybody else it is busy time.
ALTER TABLE calendar_blocks DROP CONSTRAINT IF EXISTS calendar_blocks_kind_check;
ALTER TABLE calendar_blocks ADD CONSTRAINT calendar_blocks_kind_check CHECK (kind IN ('focus', 'open', 'entry'));
ALTER TABLE calendar_blocks ADD COLUMN IF NOT EXISTS details TEXT NOT NULL DEFAULT '';
