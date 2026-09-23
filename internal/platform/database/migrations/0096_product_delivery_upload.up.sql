-- Direct repair uploads have no persistent source object. Only the verified
-- replacement destination is stored and managed by existing order cleanup.
ALTER TABLE product_delivery_repairs
 ADD COLUMN source_kind text NOT NULL DEFAULT 'stored',
 ALTER COLUMN source_backend DROP NOT NULL,
 ALTER COLUMN source_key DROP NOT NULL,
 ADD CONSTRAINT product_delivery_repair_source_kind CHECK (
   (source_kind='stored' AND source_backend IS NOT NULL AND source_key IS NOT NULL)
   OR (source_kind='upload' AND source_asset_id IS NULL AND source_backend IS NULL AND source_key IS NULL)
 );
