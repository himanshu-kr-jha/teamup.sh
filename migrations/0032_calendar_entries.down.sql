DELETE FROM calendar_blocks WHERE kind = 'entry';
ALTER TABLE calendar_blocks DROP COLUMN IF EXISTS details;
ALTER TABLE calendar_blocks DROP CONSTRAINT IF EXISTS calendar_blocks_kind_check;
ALTER TABLE calendar_blocks ADD CONSTRAINT calendar_blocks_kind_check CHECK (kind IN ('focus', 'open'));
