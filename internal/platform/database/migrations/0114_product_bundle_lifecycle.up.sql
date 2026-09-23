-- One private source projection for both old single-file and versioned bundles.
-- Never infer a historical single-file source owner from today's asset owner.
CREATE VIEW product_order_media_sources AS
 SELECT c.order_id,0 AS position,c.source_asset_id,c.root_asset_id,
   NULL::uuid AS accepted_owner_id,c.contract->'asset'->>'storageBackend' AS storage_backend,
   c.contract->'asset'->>'storageKey' AS storage_key,c.contract->'asset'->>'sourceType' AS source_type
 FROM product_order_contracts c WHERE NOT (c.contract ? 'delivery')
 UNION ALL
 SELECT c.order_id,(f->>'position')::integer,(f->>'assetId')::uuid,(f->>'assetId')::uuid,
   (f->'source'->>'ownerId')::uuid,f->'source'->>'storageBackend',f->'source'->>'storageKey',f->'source'->>'sourceType'
 FROM product_order_contracts c,
 LATERAL jsonb_array_elements(c.contract->'delivery'->'files') f
 WHERE c.contract->'delivery'->>'format'='zip-v1';

CREATE VIEW product_order_media_subjects AS
 SELECT c.order_id,o.buyer_id AS user_id FROM product_order_contracts c JOIN orders o ON o.id=c.order_id
 UNION SELECT order_id,(contract->'product'->>'sellerId')::uuid FROM product_order_contracts
 UNION SELECT order_id,accepted_owner_id FROM product_order_media_sources WHERE accepted_owner_id IS NOT NULL
 UNION SELECT s.order_id,a.owner_id FROM product_order_media_sources s JOIN assets a
 ON a.id IN(s.source_asset_id,s.root_asset_id)
 OR (a.storage_backend=s.storage_backend AND a.storage_key=s.storage_key);

CREATE OR REPLACE VIEW product_delivery_cleanup_subjects AS
 SELECT s.order_id,s.user_id FROM product_order_media_subjects s
 JOIN product_delivery_snapshots d ON d.order_id=s.order_id;

-- Receipt validation and omitted-cleanup reconciliation must recognize the
-- same historical member locations as physical cleanup, including moved files.
CREATE OR REPLACE VIEW original_media_cleanup_locations AS
 SELECT owner_id,storage_backend,storage_key FROM assets WHERE source_type IN ('generation','upload')
 UNION
 SELECT COALESCE(root.owner_id,s.accepted_owner_id),s.storage_backend,s.storage_key
 FROM product_order_media_sources s LEFT JOIN assets root ON root.id=s.root_asset_id
 WHERE s.source_type IN ('generation','upload') AND COALESCE(root.owner_id,s.accepted_owner_id) IS NOT NULL;

-- Original bundle reconstruction has no single source locator. A stored backup
-- or an uploaded exact ZIP remains a separate, explicit recovery method.
ALTER TABLE product_delivery_repairs DROP CONSTRAINT product_delivery_repair_source_kind;
ALTER TABLE product_delivery_repairs ADD CONSTRAINT product_delivery_repair_source_kind CHECK (
 (source_kind='stored' AND source_backend IS NOT NULL AND source_key IS NOT NULL)
 OR (source_kind IN ('upload','bundle') AND source_asset_id IS NULL AND source_backend IS NULL AND source_key IS NULL)
);
CREATE FUNCTION check_bundle_repair_source() RETURNS trigger AS $$
BEGIN
 IF NEW.source_kind='bundle' AND NOT EXISTS(
  SELECT 1 FROM product_delivery_snapshots WHERE order_id=NEW.order_id AND format='zip-v1'
 ) THEN RAISE EXCEPTION 'bundle reconstruction requires ZIP evidence' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_delivery_repair_bundle_source BEFORE INSERT ON product_delivery_repairs
 FOR EACH ROW EXECUTE FUNCTION check_bundle_repair_source();
