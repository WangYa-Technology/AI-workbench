-- A preview is a separately uploaded/generated sample, never the deliverable.
ALTER TABLE products ADD COLUMN preview_asset_id uuid REFERENCES assets(id);
ALTER TABLE products ADD CONSTRAINT products_distinct_preview CHECK (preview_asset_id IS NULL OR preview_asset_id<>asset_id);

-- Keep historical contract sources private even after catalog replacement/removal.
CREATE VIEW product_delivery_roots AS
 SELECT COALESCE(a.origin_asset_id,a.id) AS asset_id FROM products p JOIN assets a ON a.id=p.asset_id
 UNION SELECT root_asset_id FROM product_order_contracts
 UNION SELECT a.id FROM assets a JOIN product_order_contracts c
 ON a.storage_backend=c.contract->'asset'->>'storageBackend' AND a.storage_key=c.contract->'asset'->>'storageKey';
CREATE INDEX product_contract_storage_evidence ON product_order_contracts
 ((contract->'asset'->>'storageBackend'),(contract->'asset'->>'storageKey'));

CREATE OR REPLACE VIEW public_works AS
 SELECT w.* FROM works w
 JOIN users u ON u.id=w.author_id AND u.status='active'
 JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE w.status='published' AND (origin.id IS NULL OR origin.scan_status='clean')
 AND NOT EXISTS(SELECT 1 FROM product_delivery_roots r WHERE r.asset_id=COALESCE(a.origin_asset_id,a.id));

CREATE OR REPLACE VIEW public_products AS
 SELECT p.* FROM products p
 JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
 JOIN licenses l ON l.code=p.license_code AND l.status='active'
 JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE p.status='active' AND (a.origin_asset_id IS NULL OR origin.scan_status='clean');

CREATE VIEW public_product_previews AS
 SELECT p.id AS product_id,a.id AS asset_id,
 '/api/v1/assets/'||a.id::text||'/content' AS media_url,a.kind,a.width,a.height
 FROM public_products p JOIN assets a ON a.id=p.preview_asset_id
 WHERE a.owner_id=p.seller_id AND a.source_type IN ('upload','generation')
 AND a.origin_asset_id IS NULL AND a.scan_status='clean'
 AND NOT EXISTS(SELECT 1 FROM product_delivery_roots r WHERE r.asset_id=a.id);

CREATE INDEX products_preview_asset ON products(preview_asset_id) WHERE preview_asset_id IS NOT NULL;

-- Changing the advertised sample requires the buyer to accept the revised offer.
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
