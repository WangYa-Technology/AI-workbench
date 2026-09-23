-- Replacement locations are new immutable evidence. Never rewrite the original
-- accepted digest/location or destroy a damaged object during repair.
CREATE TABLE product_delivery_repairs (
 id uuid PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES product_delivery_snapshots(order_id),
 revision integer NOT NULL CHECK (revision>0),
 actor_id uuid NOT NULL REFERENCES users(id),
 key_sha256 text NOT NULL CHECK (key_sha256 ~ '^[0-9a-f]{64}$'),
 request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
 source_asset_id uuid REFERENCES assets(id),
 source_backend text NOT NULL CHECK (source_backend IN ('local_file','s3')),
 source_key text NOT NULL,
 storage_backend text NOT NULL CHECK (storage_backend IN ('local_file','s3')),
 storage_key text NOT NULL,
 reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 2000),
 state text NOT NULL DEFAULT 'prepared' CHECK (state IN ('prepared','ready','removed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 ready_at timestamptz,
 removed_at timestamptz,
 UNIQUE(order_id,revision), UNIQUE(actor_id,key_sha256), UNIQUE(storage_backend,storage_key),
 CHECK ((state='prepared' AND ready_at IS NULL AND removed_at IS NULL)
 OR (state='ready' AND ready_at IS NOT NULL AND removed_at IS NULL)
 OR (state='removed' AND removed_at IS NOT NULL)),
 CHECK ((storage_backend,storage_key)<>(source_backend,source_key))
);
CREATE TRIGGER product_delivery_repairs_protected BEFORE UPDATE OR DELETE ON product_delivery_repairs
 FOR EACH ROW EXECUTE FUNCTION protect_product_delivery_snapshot();

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

CREATE INDEX product_delivery_repair_sources ON product_delivery_repairs(source_backend,source_key)
 WHERE source_asset_id IS NOT NULL;

-- A repair backup cannot already be public, traded or delivered under another
-- contract. Repeat this projection after acquiring the asset lock so concurrent
-- publication/scan changes are observed in a fresh statement snapshot.
CREATE VIEW product_repair_backup_candidates AS
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

-- Repair evidence also prevents reuse as a new sale original.
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
   AND (g.mode='chat' OR g.source_asset_id IS NOT NULL OR g.mask_asset_id IS NOT NULL OR g.source_work_id IS NOT NULL OR g.source_task_id IS NOT NULL OR g.parent_generation_id IS NOT NULL OR EXISTS(SELECT 1 FROM generation_reference_assets r WHERE r.generation_id=g.id)))
 AND NOT EXISTS(SELECT 1 FROM product_delivery_repairs r WHERE r.source_asset_id=a.id
   OR (a.storage_backend=r.storage_backend AND a.storage_key=r.storage_key)
   OR (r.source_asset_id IS NOT NULL AND a.storage_backend=r.source_backend AND a.storage_key=r.source_key));
