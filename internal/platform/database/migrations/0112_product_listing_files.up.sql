-- Real, ordered sources for multi-file drafts. No historical label is turned
-- into fabricated file evidence, and single-file offers keep their exact hash.
CREATE TABLE product_listing_files (
 product_id uuid NOT NULL REFERENCES products(id),
 position smallint NOT NULL CHECK (position BETWEEN 0 AND 19),
 asset_id uuid NOT NULL REFERENCES assets(id),
 file_name text NOT NULL CHECK (octet_length(file_name) BETWEEN 1 AND 200),
 PRIMARY KEY(product_id,position),
 UNIQUE(product_id,asset_id),
 UNIQUE(product_id,file_name)
);
CREATE INDEX product_listing_files_asset ON product_listing_files(asset_id,product_id);

-- Only internal versioning reads this projection. Never return storage evidence
-- from a seller/public endpoint. Names/IDs have a separate safe projection.
CREATE VIEW product_listing_file_manifests AS
 SELECT f.product_id,jsonb_agg(jsonb_build_object(
   'position',f.position,'assetId',f.asset_id,'name',f.file_name,
   'source',jsonb_build_object('ownerId',a.owner_id,'kind',a.kind,'mimeType',a.mime_type,
     'scanStatus',a.scan_status,'storageBackend',a.storage_backend,'storageKey',a.storage_key,
     'familyId',a.family_id,'version',a.version_number,'originAssetId',a.origin_asset_id,
     'sourceType',a.source_type,'sourceId',a.source_id,'licenseCode',a.license_code)
 ) ORDER BY f.position) AS files
 FROM product_listing_files f JOIN assets a ON a.id=f.asset_id GROUP BY f.product_id;

CREATE OR REPLACE VIEW product_listing_versions AS
 SELECT p.id AS product_id,
 encode(public.digest((jsonb_build_object('offer',o.offer_version,'category',p.category)
   || CASE WHEN f.files IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('files',f.files) END)::text,'sha256'),'hex') AS content_version,
 encode(public.digest((jsonb_build_object('product',to_jsonb(p),'offer',o.offer_version,'publication',to_jsonb(m))
   || CASE WHEN f.files IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('files',f.files) END)::text,'sha256'),'hex') AS version
 FROM products p JOIN product_offers o ON o.product_id=p.id
 LEFT JOIN product_publications m ON m.product_id=p.id
 LEFT JOIN product_listing_file_manifests f ON f.product_id=p.id;

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

-- Publication/checkout remains closed until contracts and fulfillment support
-- the entire bundle. This also rejects writes from an older API process.
CREATE FUNCTION check_product_listing_files() RETURNS trigger AS $$
DECLARE
 target uuid;
 item products%ROWTYPE;
 n integer;
 first_asset uuid;
 labels jsonb;
 maximum integer;
BEGIN
 IF TG_TABLE_NAME='products' THEN target:=NEW.id;
 ELSIF TG_OP='DELETE' THEN target:=OLD.product_id;
 ELSE target:=NEW.product_id;
 END IF;
 SELECT * INTO item FROM products WHERE id=target FOR UPDATE;
 IF NOT FOUND THEN RETURN NULL; END IF;
 SELECT count(*),max(position),jsonb_agg(file_name ORDER BY position)
 INTO n,maximum,labels FROM product_listing_files WHERE product_id=target;
 IF n=0 THEN RETURN NULL; END IF;
 SELECT asset_id INTO first_asset FROM product_listing_files WHERE product_id=target AND position=0;
 IF n<2 OR n>20 OR maximum<>n-1 OR first_asset IS DISTINCT FROM item.asset_id
 OR labels IS DISTINCT FROM item.included_files
 OR EXISTS(SELECT 1 FROM product_listing_files WHERE product_id=target AND asset_id=item.preview_asset_id) THEN
  RAISE EXCEPTION 'invalid ordered product file list' USING ERRCODE='23514';
 END IF;
 IF item.status='active' THEN
  RAISE EXCEPTION 'multi-file product fulfillment is not enabled' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END;
$$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER product_listing_files_consistent AFTER INSERT OR UPDATE OR DELETE ON product_listing_files
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_product_listing_files();
CREATE CONSTRAINT TRIGGER product_listing_files_product_consistent AFTER INSERT OR UPDATE ON products
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_product_listing_files();

-- File identities are replaced as a complete list under the product lock;
-- prohibit moving a row to another product without checking the old product.
CREATE FUNCTION reject_product_listing_file_move() RETURNS trigger AS $$
BEGIN
 IF NEW.product_id<>OLD.product_id THEN
  RAISE EXCEPTION 'product file identity cannot move' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_listing_files_no_move BEFORE UPDATE ON product_listing_files
 FOR EACH ROW EXECUTE FUNCTION reject_product_listing_file_move();

-- A bundle member is not an independent repair backup.
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

-- Public readers also fail closed before a bundle delivery protocol exists.
CREATE OR REPLACE VIEW public_products AS
 SELECT p.* FROM products p
 JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
 JOIN licenses l ON l.code=p.license_code AND l.status='active'
 JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE p.status='active' AND NOT EXISTS(SELECT 1 FROM product_listing_files f WHERE f.product_id=p.id) AND (a.origin_asset_id IS NULL OR origin.scan_status='clean')
 AND (NOT EXISTS(SELECT 1 FROM product_publications m WHERE m.product_id=p.id)
 OR EXISTS(SELECT 1 FROM product_publications m JOIN product_listing_versions v ON v.product_id=m.product_id
   WHERE m.product_id=p.id AND m.review_status='approved' AND m.approved_version=v.content_version
   AND EXISTS(SELECT 1 FROM product_source_candidates candidate WHERE candidate.asset_id=p.asset_id AND candidate.owner_id=p.seller_id)));
