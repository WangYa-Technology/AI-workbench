DROP INDEX IF EXISTS generations_parent_generation_idx;
ALTER TABLE generations DROP COLUMN IF EXISTS parent_generation_id;
