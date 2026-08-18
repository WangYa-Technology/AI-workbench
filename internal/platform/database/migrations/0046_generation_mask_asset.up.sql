ALTER TABLE generations
  ADD COLUMN mask_asset_id uuid REFERENCES assets(id);

CREATE INDEX generations_mask_asset_idx
  ON generations (mask_asset_id)
  WHERE mask_asset_id IS NOT NULL;
