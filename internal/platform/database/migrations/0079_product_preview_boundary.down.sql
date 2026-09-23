-- Rolling back must not silently republish deliverables or discard configured previews.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM products) OR EXISTS(SELECT 1 FROM product_order_contracts) THEN
  RAISE EXCEPTION 'product preview boundary requires an explicit data-safe rollback plan';
 END IF;
END $$;
DROP VIEW public_product_previews;
CREATE OR REPLACE VIEW public_works AS
 SELECT w.* FROM works w
 JOIN users u ON u.id=w.author_id AND u.status='active'
 JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE w.status='published' AND (origin.id IS NULL OR origin.scan_status='clean');
DROP VIEW product_delivery_roots;
DROP INDEX product_contract_storage_evidence;
DROP VIEW public_products;
-- Retain the original projection shape without a preview-dependent expression.
CREATE OR REPLACE VIEW product_offers AS
SELECT product_id,source_asset_id,root_asset_id,contract,
       encode(public.digest(contract::text,'sha256'),'hex') AS offer_version
FROM (
  SELECT p.id AS product_id,a.id AS source_asset_id,root.id AS root_asset_id,
    jsonb_build_object(
      'product',jsonb_build_object('id',p.id,'sellerId',p.seller_id,'title',p.title,
        'description',p.description,'productType',p.product_type,'priceCents',p.price_cents,
        'currency',p.currency,'aiDisclosure',p.ai_disclosure,'includedFiles',p.included_files,'compatibility',p.compatibility),
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
ALTER TABLE products DROP COLUMN preview_asset_id;
CREATE VIEW public_products AS
 SELECT p.* FROM products p
 JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
 JOIN licenses l ON l.code=p.license_code AND l.status='active'
 JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE p.status='active' AND (a.origin_asset_id IS NULL OR origin.scan_status='clean');
