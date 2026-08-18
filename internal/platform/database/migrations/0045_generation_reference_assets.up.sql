CREATE TABLE generation_reference_assets (
  generation_id uuid NOT NULL REFERENCES generations(id) ON DELETE CASCADE,
  asset_id uuid NOT NULL REFERENCES assets(id),
  position integer NOT NULL CHECK (position BETWEEN 0 AND 7),
  PRIMARY KEY (generation_id, position),
  UNIQUE (generation_id, asset_id)
);

INSERT INTO generation_reference_assets(generation_id,asset_id,position)
SELECT id,source_asset_id,0 FROM generations WHERE source_asset_id IS NOT NULL;

CREATE INDEX generation_reference_assets_asset_idx
  ON generation_reference_assets (asset_id, generation_id);
