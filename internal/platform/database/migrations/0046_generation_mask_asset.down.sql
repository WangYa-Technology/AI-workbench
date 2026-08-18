DROP INDEX IF EXISTS generations_mask_asset_idx;
ALTER TABLE generations DROP COLUMN IF EXISTS mask_asset_id;
