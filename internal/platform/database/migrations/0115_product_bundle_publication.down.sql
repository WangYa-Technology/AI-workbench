DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_listing_commands WHERE action IN ('submit','approve')
    AND jsonb_array_length(COALESCE(snapshot->'files','[]'::jsonb))>0)
 OR EXISTS(SELECT 1 FROM products p JOIN product_listing_files f ON f.product_id=p.id WHERE p.status='active')
 OR EXISTS(SELECT 1 FROM product_order_contracts WHERE contract ? 'delivery') THEN
  RAISE EXCEPTION 'bundle publication or accepted evidence prevents rollback';
 END IF;
END $$;
DROP TRIGGER entitlements_bundle_shape ON entitlements;
DROP FUNCTION check_bundle_entitlement();
DROP TRIGGER assets_bundle_shape ON assets;
DROP FUNCTION check_bundle_purchase_asset();
DROP TRIGGER product_publication_bundle_protocol ON product_publications;
DROP FUNCTION check_bundle_publication_protocol();
DROP TRIGGER product_contract_bundle_protocol ON product_order_contracts;
DROP FUNCTION check_bundle_contract_protocol();
DROP TRIGGER jobs_bundle_protocol ON jobs;
DROP FUNCTION check_bundle_worker_protocol();
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
 IF item.status='active' THEN
  RAISE EXCEPTION 'multi-file product fulfillment is not enabled' USING ERRCODE='23514';
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
 WHERE p.status='active' AND NOT EXISTS(SELECT 1 FROM product_listing_files f WHERE f.product_id=p.id) AND (a.origin_asset_id IS NULL OR origin.scan_status='clean')
 AND (NOT EXISTS(SELECT 1 FROM product_publications m WHERE m.product_id=p.id)
 OR EXISTS(SELECT 1 FROM product_publications m JOIN product_listing_versions v ON v.product_id=m.product_id
   WHERE m.product_id=p.id AND m.review_status='approved' AND m.approved_version=v.content_version
   AND EXISTS(SELECT 1 FROM product_source_candidates candidate WHERE candidate.asset_id=p.asset_id AND candidate.owner_id=p.seller_id)));

DROP FUNCTION require_product_bundle_protocol();
