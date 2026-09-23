-- Immutable bundle contracts and their evidence must never be discarded.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_order_contracts WHERE contract ? 'delivery')
 OR EXISTS(SELECT 1 FROM product_delivery_snapshots WHERE format<>'single') THEN
  RAISE EXCEPTION 'bundle delivery evidence prevents rollback';
 END IF;
END $$;
DROP TRIGGER product_contract_bundle_snapshot ON product_order_contracts;
DROP TRIGGER product_snapshot_bundle_contract ON product_delivery_snapshots;
DROP FUNCTION check_product_bundle_snapshot();
ALTER TABLE product_delivery_snapshots DROP CONSTRAINT product_delivery_format,
 DROP COLUMN format, DROP COLUMN file_manifest,
 ALTER COLUMN source_backend SET NOT NULL, ALTER COLUMN source_key SET NOT NULL;
CREATE OR REPLACE VIEW product_offers AS
SELECT product_id,source_asset_id,root_asset_id,contract,
       encode(public.digest(contract::text,'sha256'),'hex') AS offer_version
FROM (
  SELECT p.id AS product_id,a.id AS source_asset_id,root.id AS root_asset_id,
    jsonb_build_object(
      'product',jsonb_build_object('id',p.id,'sellerId',p.seller_id,'title',p.title,
        'description',p.description,'productType',p.product_type,'priceCents',p.price_cents,
        'currency',p.currency,'aiDisclosure',p.ai_disclosure,'includedFiles',p.included_files,'compatibility',p.compatibility)
        || CASE WHEN p.preview_asset_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('previewAssetId',p.preview_asset_id) END,
      'license',jsonb_build_object('code',l.code,'name',l.name,'summary',l.summary,'version',l.version,'terms',l.terms,
        'allowsCommercial',l.allows_commercial,'allowsDerivatives',l.allows_derivatives,
        'allowsRedistribution',l.allows_redistribution,'attributionRequired',l.attribution_required,
        'refundWindowDays',l.refund_window_days),
      'asset',jsonb_build_object('id',a.id,'rootId',root.id,'familyId',a.family_id,'version',a.version_number,
        'title',a.title,'kind',a.kind,'mimeType',a.mime_type,'width',a.width,'height',a.height,
        'storageBackend',root.storage_backend,'storageKey',root.storage_key,'sourceType',root.source_type)
    ) AS contract
  FROM products p JOIN licenses l ON l.code=p.license_code
  JOIN assets a ON a.id=p.asset_id JOIN assets root ON root.id=COALESCE(a.origin_asset_id,a.id)
) offers;

CREATE OR REPLACE VIEW product_delivery_roots AS
 SELECT COALESCE(a.origin_asset_id,a.id) AS asset_id FROM products p JOIN assets a ON a.id=p.asset_id
 UNION SELECT root_asset_id FROM product_order_contracts
 UNION SELECT a.id FROM assets a JOIN product_order_contracts c
 ON a.storage_backend=c.contract->'asset'->>'storageBackend' AND a.storage_key=c.contract->'asset'->>'storageKey'
 UNION SELECT a.id FROM assets a JOIN product_delivery_snapshots d
 ON a.storage_backend=d.storage_backend AND a.storage_key=d.storage_key
 UNION SELECT a.id FROM assets a JOIN product_delivery_repairs r
 ON a.storage_backend=r.storage_backend AND a.storage_key=r.storage_key
 UNION SELECT source_asset_id FROM product_delivery_repairs WHERE source_asset_id IS NOT NULL
 UNION SELECT a.id FROM assets a JOIN product_delivery_repairs r
 ON r.source_asset_id IS NOT NULL AND a.storage_backend=r.source_backend AND a.storage_key=r.source_key
 UNION SELECT f.asset_id FROM product_listing_files f
 UNION SELECT COALESCE(a.origin_asset_id,a.id) FROM product_listing_files f JOIN assets a ON a.id=f.asset_id
 UNION SELECT alias.id FROM product_listing_files f JOIN assets a ON a.id=f.asset_id
 JOIN assets alias ON alias.storage_backend=a.storage_backend AND alias.storage_key=a.storage_key;

CREATE OR REPLACE VIEW product_repair_backup_candidates AS
 SELECT a.id AS asset_id,a.owner_id FROM assets a
 JOIN users u ON u.id=a.owner_id AND u.status='active'
 WHERE a.source_type='upload' AND a.origin_asset_id IS NULL AND a.scan_status='clean'
 AND a.license_code IS DISTINCT FROM 'task-contract'
 AND a.storage_backend IN ('local_file','s3') AND length(a.storage_key)>0
 AND NOT EXISTS(SELECT 1 FROM assets alias WHERE alias.id<>a.id
   AND alias.storage_backend=a.storage_backend AND alias.storage_key=a.storage_key)
 AND NOT EXISTS(SELECT 1 FROM works w WHERE w.asset_id=a.id AND w.status<>'removed')
 AND NOT EXISTS(SELECT 1 FROM products p WHERE p.asset_id=a.id OR p.preview_asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM delivery_assets da WHERE da.asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM task_delivery_grants g WHERE g.source_asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM product_order_contracts c WHERE a.id IN (c.source_asset_id,c.root_asset_id)
   OR (a.storage_backend=c.contract->'asset'->>'storageBackend' AND a.storage_key=c.contract->'asset'->>'storageKey'))
 AND NOT EXISTS(SELECT 1 FROM product_delivery_snapshots d WHERE a.storage_backend=d.storage_backend AND a.storage_key=d.storage_key)
 AND NOT EXISTS(SELECT 1 FROM product_delivery_repairs r WHERE a.storage_backend=r.storage_backend AND a.storage_key=r.storage_key)
 AND NOT EXISTS(SELECT 1 FROM product_listing_files f JOIN assets original ON original.id=f.asset_id
   WHERE f.asset_id=a.id OR (original.storage_backend=a.storage_backend AND original.storage_key=a.storage_key));
