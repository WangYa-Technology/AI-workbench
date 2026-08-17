ALTER TABLE generations DROP CONSTRAINT IF EXISTS generations_charged_within_estimate;
ALTER TABLE generation_commands DROP COLUMN IF EXISTS request_hash;
