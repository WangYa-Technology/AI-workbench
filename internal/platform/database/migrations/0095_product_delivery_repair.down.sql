DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_delivery_repairs) THEN
  RAISE EXCEPTION 'cannot remove delivery repair evidence';
 END IF;
END $$;
CREATE OR REPLACE VIEW product_delivery_roots AS
 SELECT COALESCE(a.origin_asset_id,a.id) AS asset_id FROM products p JOIN assets a ON a.id=p.asset_id
 UNION SELECT root_asset_id FROM product_order_contracts
 UNION SELECT a.id FROM assets a JOIN product_order_contracts c
 ON a.storage_backend=c.contract->'asset'->>'storageBackend' AND a.storage_key=c.contract->'asset'->>'storageKey'
 UNION SELECT a.id FROM assets a JOIN product_delivery_snapshots d
 ON a.storage_backend=d.storage_backend AND a.storage_key=d.storage_key;
CREATE OR REPLACE VIEW product_source_candidates AS
 SELECT a.id AS asset_id,a.owner_id FROM assets a
 JOIN users u ON u.id=a.owner_id AND u.status='active'
 WHERE a.source_type IN ('upload','generation') AND a.origin_asset_id IS NULL
 AND a.scan_status='clean' AND a.license_code IS DISTINCT FROM 'task-contract'
 AND a.storage_backend IN ('local_file','s3') AND length(a.storage_key)>0
 AND (a.source_type='upload' OR EXISTS(SELECT 1 FROM generations g WHERE g.id=a.source_id AND g.output_asset_id=a.id AND g.owner_id=a.owner_id AND g.status='succeeded'))
 AND NOT EXISTS(SELECT 1 FROM assets alias WHERE alias.id<>a.id
   AND alias.storage_backend=a.storage_backend AND alias.storage_key=a.storage_key)
 AND NOT EXISTS(SELECT 1 FROM works w WHERE w.asset_id=a.id AND w.status<>'removed')
 AND NOT EXISTS(SELECT 1 FROM delivery_assets da WHERE da.asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM task_delivery_grants grant_record WHERE grant_record.source_asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM products p WHERE p.preview_asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM product_delivery_snapshots d WHERE d.storage_backend=a.storage_backend AND d.storage_key=a.storage_key)
 AND NOT EXISTS(SELECT 1 FROM generations g WHERE (g.id=a.source_id OR g.output_asset_id=a.id)
   AND (g.mode='chat' OR g.source_asset_id IS NOT NULL OR g.mask_asset_id IS NOT NULL OR g.source_work_id IS NOT NULL OR g.source_task_id IS NOT NULL OR g.parent_generation_id IS NOT NULL OR EXISTS(SELECT 1 FROM generation_reference_assets r WHERE r.generation_id=g.id)));
DROP VIEW product_repair_backup_candidates;
DROP TABLE product_delivery_repairs;
