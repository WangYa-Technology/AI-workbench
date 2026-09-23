-- Stop/drain old API and worker instances before this migration. Database
-- capability declarations prevent old writers/claimers, not in-flight external IO.
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before enabling bundle publication';
 END IF;
END $$;

CREATE FUNCTION require_product_bundle_protocol() RETURNS void AS $$
BEGIN
 IF current_setting('app.product_bundle_protocol',true) IS DISTINCT FROM 'zip-v1' THEN
  RAISE EXCEPTION 'bundle-aware application required' USING ERRCODE='23514';
 END IF;
END;
$$ LANGUAGE plpgsql;

-- Old workers cannot claim or renew jobs after cutover, including account
-- cleanup/export jobs whose payload does not contain a product/order ID.
CREATE FUNCTION check_bundle_worker_protocol() RETURNS trigger AS $$
BEGIN
 IF NEW.status='running' OR (TG_OP='UPDATE' AND OLD.status='running') THEN
  PERFORM require_product_bundle_protocol();
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER jobs_bundle_protocol BEFORE INSERT OR UPDATE ON jobs
 FOR EACH ROW EXECUTE FUNCTION check_bundle_worker_protocol();

CREATE FUNCTION check_bundle_contract_protocol() RETURNS trigger AS $$
BEGIN
 IF NEW.contract ? 'delivery' THEN PERFORM require_product_bundle_protocol(); END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_contract_bundle_protocol BEFORE INSERT ON product_order_contracts
 FOR EACH ROW EXECUTE FUNCTION check_bundle_contract_protocol();

CREATE FUNCTION check_bundle_publication_protocol() RETURNS trigger AS $$
BEGIN
 IF NEW.review_status IN ('pending','approved') AND EXISTS(
  SELECT 1 FROM product_listing_files WHERE product_id=NEW.product_id
 ) THEN PERFORM require_product_bundle_protocol(); END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_publication_bundle_protocol BEFORE INSERT OR UPDATE ON product_publications
 FOR EACH ROW EXECUTE FUNCTION check_bundle_publication_protocol();

-- A purchase asset for a bundle cannot impersonate one source image/document.
-- Check current row shape on every update, while allowing scan/refund/deletion
-- metadata changes; readiness is required only when initially granting rights.
CREATE FUNCTION check_bundle_purchase_asset() RETURNS trigger AS $$
DECLARE c jsonb; d product_delivery_snapshots%ROWTYPE; buyer uuid;
BEGIN
 IF TG_OP='UPDATE' AND OLD.source_type='purchase' AND EXISTS(
  SELECT 1 FROM product_order_contracts WHERE order_id=OLD.source_id AND contract ? 'delivery'
 ) AND (NEW.source_type IS DISTINCT FROM OLD.source_type OR NEW.source_id IS DISTINCT FROM OLD.source_id) THEN
  RAISE EXCEPTION 'accepted bundle asset identity cannot change' USING ERRCODE='23514';
 END IF;
 IF NEW.source_type<>'purchase' THEN RETURN NEW; END IF;
 SELECT contract INTO c FROM product_order_contracts WHERE order_id=NEW.source_id;
 IF NOT COALESCE(c ? 'delivery',false) THEN RETURN NEW; END IF;
 PERFORM require_product_bundle_protocol();
 SELECT * INTO d FROM product_delivery_snapshots WHERE order_id=NEW.source_id;
 SELECT buyer_id INTO buyer FROM orders WHERE id=NEW.source_id;
 IF NEW.kind IS DISTINCT FROM 'document' OR NEW.mime_type IS DISTINCT FROM 'application/zip'
 OR NEW.origin_asset_id IS NOT NULL OR NEW.width IS NOT NULL OR NEW.height IS NOT NULL
 OR NEW.owner_id IS DISTINCT FROM buyer OR NEW.size_bytes IS DISTINCT FROM d.size_bytes
 OR d.format IS DISTINCT FROM 'zip-v1' OR NEW.license_code IS DISTINCT FROM c->'license'->>'code' THEN
  RAISE EXCEPTION 'purchase must represent complete accepted bundle' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER assets_bundle_shape BEFORE INSERT OR UPDATE ON assets
 FOR EACH ROW EXECUTE FUNCTION check_bundle_purchase_asset();

CREATE FUNCTION check_bundle_entitlement() RETURNS trigger AS $$
DECLARE c jsonb;
BEGIN
 IF TG_OP='UPDATE' AND EXISTS(
  SELECT 1 FROM product_order_contracts WHERE order_id=OLD.order_id AND contract ? 'delivery'
 ) AND (NEW.order_id IS DISTINCT FROM OLD.order_id OR NEW.asset_id IS DISTINCT FROM OLD.asset_id
 OR NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.product_id IS DISTINCT FROM OLD.product_id
 OR NEW.license_code IS DISTINCT FROM OLD.license_code) THEN
  RAISE EXCEPTION 'accepted bundle entitlement identity cannot change' USING ERRCODE='23514';
 END IF;
 SELECT contract INTO c FROM product_order_contracts WHERE order_id=NEW.order_id;
 IF NOT COALESCE(c ? 'delivery',false) THEN RETURN NEW; END IF;
 PERFORM require_product_bundle_protocol();
 IF NEW.status='active' AND (TG_OP='INSERT' OR OLD.status IS DISTINCT FROM 'active') AND NOT EXISTS(
  SELECT 1 FROM orders o JOIN assets a ON a.id=NEW.asset_id
  JOIN product_delivery_snapshots d ON d.order_id=o.id
  WHERE o.id=NEW.order_id AND o.buyer_id=NEW.user_id AND o.product_id=NEW.product_id
  AND a.owner_id=NEW.user_id AND a.source_type='purchase' AND a.source_id=o.id
  AND a.kind='document' AND a.mime_type='application/zip' AND a.origin_asset_id IS NULL
  AND a.scan_status='clean' AND d.format='zip-v1' AND d.state='ready' AND a.size_bytes=d.size_bytes
  AND NEW.license_code=c->'license'->>'code'
 ) THEN
  RAISE EXCEPTION 'bundle entitlement requires complete ready buyer delivery' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER entitlements_bundle_shape BEFORE INSERT OR UPDATE ON entitlements
 FOR EACH ROW EXECUTE FUNCTION check_bundle_entitlement();

CREATE OR REPLACE FUNCTION check_product_listing_files() RETURNS trigger AS $$
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
 PERFORM require_product_bundle_protocol();
 IF item.status='active' AND NOT EXISTS(
  SELECT 1 FROM product_publications m JOIN product_listing_versions v ON v.product_id=m.product_id
  WHERE m.product_id=target AND m.review_status='approved' AND m.approved_version=v.content_version
 ) THEN
  RAISE EXCEPTION 'bundle requires current complete approval' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE VIEW public_products AS
 SELECT p.* FROM products p
 JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
 JOIN licenses l ON l.code=p.license_code AND l.status='active'
 JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE p.status='active' AND (a.origin_asset_id IS NULL OR origin.scan_status='clean')
 AND ((NOT EXISTS(SELECT 1 FROM product_publications m WHERE m.product_id=p.id) AND NOT EXISTS(SELECT 1 FROM product_listing_files f WHERE f.product_id=p.id))
 OR EXISTS(SELECT 1 FROM product_publications m JOIN product_listing_versions v ON v.product_id=m.product_id
   WHERE m.product_id=p.id AND m.review_status='approved' AND m.approved_version=v.content_version
   AND EXISTS(SELECT 1 FROM product_source_candidates candidate WHERE candidate.asset_id=p.asset_id AND candidate.owner_id=p.seller_id)
   AND NOT EXISTS(SELECT 1 FROM product_listing_files f LEFT JOIN product_source_candidates c
    ON c.asset_id=f.asset_id AND c.owner_id=p.seller_id WHERE f.product_id=p.id AND c.asset_id IS NULL)));
