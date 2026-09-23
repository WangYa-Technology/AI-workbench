-- Freeze every source in the offer without changing existing single-file hashes.
-- Publication remains gated by 0112 until fulfillment and retention are wired.
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
    ) || CASE WHEN files.files IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('delivery',jsonb_build_object('format','zip-v1','files',files.files)) END AS contract
  FROM products p JOIN licenses l ON l.code=p.license_code
  JOIN assets a ON a.id=p.asset_id JOIN assets root ON root.id=COALESCE(a.origin_asset_id,a.id)
  LEFT JOIN product_listing_file_manifests files ON files.product_id=p.id
) offers;

ALTER TABLE product_delivery_snapshots
 ADD COLUMN format text NOT NULL DEFAULT 'single' CHECK (format IN ('single','zip-v1')),
 ADD COLUMN file_manifest jsonb,
 ALTER COLUMN source_backend DROP NOT NULL,
 ALTER COLUMN source_key DROP NOT NULL,
 ADD CONSTRAINT product_delivery_format CHECK (
  (format='single' AND source_backend IS NOT NULL AND source_key IS NOT NULL AND file_manifest IS NULL)
  OR (format='zip-v1' AND source_backend IS NULL AND source_key IS NULL AND mime_type='application/zip'
      AND file_manifest IS NOT NULL AND jsonb_typeof(file_manifest)='object')
 );

-- A committed bundle must have a complete matching snapshot. Old writers cannot
-- reserve a single file for a bundle; old readers fail on its NULL source locator.
CREATE FUNCTION check_product_bundle_snapshot() RETURNS trigger AS $$
DECLARE
 target uuid := NEW.order_id;
 c jsonb;
 d product_delivery_snapshots%ROWTYPE;
 source jsonb;
 member jsonb;
 i integer;
 n integer;
 names jsonb;
 total_bytes bigint := 0;
BEGIN
 SELECT contract INTO c FROM product_order_contracts WHERE order_id=target;
 SELECT * INTO d FROM product_delivery_snapshots WHERE order_id=target;
 IF NOT (c ? 'delivery') THEN
  IF d.format='zip-v1' THEN RAISE EXCEPTION 'bundle snapshot requires bundle contract' USING ERRCODE='23514'; END IF;
  IF TG_TABLE_NAME='product_order_contracts' AND EXISTS(
    SELECT 1 FROM orders o JOIN product_listing_files f ON f.product_id=o.product_id WHERE o.id=target
  ) THEN RAISE EXCEPTION 'multi-file order requires full bundle contract' USING ERRCODE='23514'; END IF;
  RETURN NULL;
 END IF;
 IF (c->'delivery'->>'format'='zip-v1' AND jsonb_typeof(c->'delivery'->'files')='array') IS DISTINCT FROM true THEN
  RAISE EXCEPTION 'unsupported bundle contract' USING ERRCODE='23514';
 END IF;
 n := jsonb_array_length(c->'delivery'->'files');
 IF n<2 OR n>20 OR d.format IS DISTINCT FROM 'zip-v1'
 OR NOT EXISTS(SELECT 1 FROM orders WHERE id=target AND delivery_snapshot_required)
 OR (d.file_manifest->'version'='1'::jsonb AND jsonb_typeof(d.file_manifest->'files')='array') IS DISTINCT FROM true THEN
  RAISE EXCEPTION 'bundle delivery evidence incomplete' USING ERRCODE='23514';
 END IF;
 IF jsonb_array_length(d.file_manifest->'files')<>n THEN
  RAISE EXCEPTION 'bundle delivery member count mismatch' USING ERRCODE='23514';
 END IF;
 FOR i IN 0..n-1 LOOP
  source := c->'delivery'->'files'->i;
  member := d.file_manifest->'files'->i;
  IF (source->'position'=to_jsonb(i) AND (source->>'assetId')::uuid IS NOT NULL
    AND (source->>'assetId')::uuid<>'00000000-0000-0000-0000-000000000000'::uuid
    AND source->'source'->>'ownerId'=c->'product'->>'sellerId'
    AND source->'source'->>'sourceType' IN ('upload','generation')
    AND source->'source'->>'scanStatus'='clean'
    AND source->'source'->>'storageBackend' IN ('local_file','s3')
    AND length(source->'source'->>'storageKey') BETWEEN 1 AND 1024
    AND COALESCE(source->'source'->'originAssetId','null'::jsonb)='null'::jsonb
    AND member->>'name'=source->>'name'
    AND octet_length(member->>'name') BETWEEN 1 AND 200
    AND member->>'mimeType'=source->'source'->>'mimeType'
    AND length(member->>'mimeType') BETWEEN 1 AND 200
    AND member->>'sha256' ~ '^[0-9a-f]{64}$'
    AND jsonb_typeof(member->'sizeBytes')='number'
    AND (member->>'sizeBytes')::bigint BETWEEN 1 AND 104857600) IS DISTINCT FROM true THEN
   RAISE EXCEPTION 'invalid frozen bundle member' USING ERRCODE='23514';
  END IF;
  IF source->'source'->>'storageBackend'=d.storage_backend AND source->'source'->>'storageKey'=d.storage_key THEN
   RAISE EXCEPTION 'bundle destination overlaps original' USING ERRCODE='23514';
  END IF;
  total_bytes := total_bytes+(member->>'sizeBytes')::bigint;
 END LOOP;
 SELECT jsonb_agg(f->>'name' ORDER BY ord) INTO names
 FROM jsonb_array_elements(c->'delivery'->'files') WITH ORDINALITY AS items(f,ord);
 IF names IS DISTINCT FROM c->'product'->'includedFiles'
 OR c->'delivery'->'files'->0->>'assetId' IS DISTINCT FROM c->'asset'->>'id'
 OR c->'asset'->>'rootId' IS DISTINCT FROM c->'asset'->>'id'
 OR c->'delivery'->'files'->0->'source'->>'storageBackend' IS DISTINCT FROM c->'asset'->>'storageBackend'
 OR c->'delivery'->'files'->0->'source'->>'storageKey' IS DISTINCT FROM c->'asset'->>'storageKey'
 OR total_bytes>=d.size_bytes
 OR (SELECT count(DISTINCT f->>'assetId') FROM jsonb_array_elements(c->'delivery'->'files') f)<>n
 OR (SELECT count(DISTINCT f->>'name') FROM jsonb_array_elements(c->'delivery'->'files') f)<>n
 OR (SELECT count(DISTINCT (f->'source'->>'storageBackend',f->'source'->>'storageKey')) FROM jsonb_array_elements(c->'delivery'->'files') f)<>n THEN
  RAISE EXCEPTION 'bundle contract and snapshot disagree' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END;
$$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER product_contract_bundle_snapshot AFTER INSERT ON product_order_contracts
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_product_bundle_snapshot();
CREATE CONSTRAINT TRIGGER product_snapshot_bundle_contract AFTER INSERT ON product_delivery_snapshots
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_product_bundle_snapshot();

-- Keep historical members private after the seller edits/removes a draft list.
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
 JOIN assets alias ON alias.storage_backend=a.storage_backend AND alias.storage_key=a.storage_key
 UNION SELECT (f->>'assetId')::uuid FROM product_order_contracts c,
 LATERAL jsonb_array_elements(COALESCE(c.contract->'delivery'->'files','[]'::jsonb)) f
 UNION SELECT a.id FROM product_order_contracts c,
 LATERAL jsonb_array_elements(COALESCE(c.contract->'delivery'->'files','[]'::jsonb)) f
 JOIN assets a ON a.storage_backend=f->'source'->>'storageBackend' AND a.storage_key=f->'source'->>'storageKey';

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
   WHERE f.asset_id=a.id OR (original.storage_backend=a.storage_backend AND original.storage_key=a.storage_key))
 AND NOT EXISTS(SELECT 1 FROM product_order_contracts c,
   LATERAL jsonb_array_elements(COALESCE(c.contract->'delivery'->'files','[]'::jsonb)) f
   WHERE a.id=(f->>'assetId')::uuid OR (a.storage_backend=f->'source'->>'storageBackend' AND a.storage_key=f->'source'->>'storageKey'));
