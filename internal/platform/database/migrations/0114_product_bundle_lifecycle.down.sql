-- Rolling back would exclude secondary originals and subjects from retention.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_order_contracts WHERE contract ? 'delivery')
 OR EXISTS(SELECT 1 FROM product_delivery_repairs WHERE source_kind='bundle') THEN
  RAISE EXCEPTION 'bundle lifecycle evidence prevents rollback';
 END IF;
END $$;
DROP TRIGGER product_delivery_repair_bundle_source ON product_delivery_repairs;
DROP FUNCTION check_bundle_repair_source();
ALTER TABLE product_delivery_repairs DROP CONSTRAINT product_delivery_repair_source_kind;
ALTER TABLE product_delivery_repairs ADD CONSTRAINT product_delivery_repair_source_kind CHECK (
 (source_kind='stored' AND source_backend IS NOT NULL AND source_key IS NOT NULL)
 OR (source_kind='upload' AND source_asset_id IS NULL AND source_backend IS NULL AND source_key IS NULL)
);
CREATE OR REPLACE VIEW product_delivery_cleanup_subjects AS
 SELECT d.order_id,o.buyer_id AS user_id FROM product_delivery_snapshots d JOIN orders o ON o.id=d.order_id
 UNION SELECT d.order_id,(c.contract->'product'->>'sellerId')::uuid
 FROM product_delivery_snapshots d JOIN product_order_contracts c ON c.order_id=d.order_id
 UNION SELECT d.order_id,a.owner_id FROM product_delivery_snapshots d
 JOIN product_order_contracts c ON c.order_id=d.order_id JOIN assets a ON a.id IN(c.source_asset_id,c.root_asset_id);
CREATE OR REPLACE VIEW original_media_cleanup_locations AS
 SELECT owner_id,storage_backend,storage_key FROM assets WHERE source_type IN ('generation','upload')
 UNION
 SELECT root.owner_id,c.contract->'asset'->>'storageBackend',c.contract->'asset'->>'storageKey'
 FROM product_order_contracts c JOIN assets root ON root.id=c.root_asset_id
 WHERE root.source_type IN ('generation','upload');
DROP VIEW product_order_media_subjects;
DROP VIEW product_order_media_sources;
