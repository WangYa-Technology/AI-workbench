-- Never erase real file-list or command evidence to run an older application.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_listing_files)
 OR EXISTS(SELECT 1 FROM product_listing_commands WHERE jsonb_array_length(COALESCE(snapshot->'files','[]'::jsonb))>0) THEN
  RAISE EXCEPTION 'product file evidence prevents rollback';
 END IF;
END $$;
CREATE OR REPLACE VIEW public_products AS
 SELECT p.* FROM products p
 JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
 JOIN licenses l ON l.code=p.license_code AND l.status='active'
 JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE p.status='active' AND (a.origin_asset_id IS NULL OR origin.scan_status='clean')
 AND (NOT EXISTS(SELECT 1 FROM product_publications m WHERE m.product_id=p.id)
 OR EXISTS(SELECT 1 FROM product_publications m JOIN product_listing_versions v ON v.product_id=m.product_id
   WHERE m.product_id=p.id AND m.review_status='approved' AND m.approved_version=v.content_version
   AND EXISTS(SELECT 1 FROM product_source_candidates candidate WHERE candidate.asset_id=p.asset_id AND candidate.owner_id=p.seller_id)));
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
 AND NOT EXISTS(SELECT 1 FROM product_delivery_repairs r WHERE a.storage_backend=r.storage_backend AND a.storage_key=r.storage_key);
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
 ON r.source_asset_id IS NOT NULL AND a.storage_backend=r.source_backend AND a.storage_key=r.source_key;
CREATE OR REPLACE VIEW product_listing_versions AS
 SELECT p.id AS product_id,
 encode(public.digest(jsonb_build_object('offer',o.offer_version,'category',p.category)::text,'sha256'),'hex') AS content_version,
 encode(public.digest(jsonb_build_object('product',to_jsonb(p),'offer',o.offer_version,'publication',to_jsonb(m))::text,'sha256'),'hex') AS version
 FROM products p JOIN product_offers o ON o.product_id=p.id
 LEFT JOIN product_publications m ON m.product_id=p.id;
DROP TRIGGER product_listing_files_product_consistent ON products;
DROP VIEW product_listing_file_manifests;
DROP TABLE product_listing_files;
DROP FUNCTION check_product_listing_files();
DROP FUNCTION reject_product_listing_file_move();
