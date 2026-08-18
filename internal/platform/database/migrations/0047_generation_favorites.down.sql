DROP INDEX IF EXISTS generations_owner_favorites_idx;
ALTER TABLE generations DROP COLUMN IF EXISTS favorited_at;
