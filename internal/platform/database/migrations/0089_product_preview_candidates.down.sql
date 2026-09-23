-- Restore the identical public predicate before dropping the shared projection.
-- No selection, contract, order, or recovery evidence is discarded.
CREATE OR REPLACE VIEW public_product_previews AS
 SELECT p.id AS product_id,a.id AS asset_id,
 '/api/v1/assets/'||a.id::text||'/content' AS media_url,a.kind,a.width,a.height
 FROM public_products p JOIN assets a ON a.id=p.preview_asset_id
 WHERE a.owner_id=p.seller_id AND a.source_type IN ('upload','generation')
 AND a.origin_asset_id IS NULL AND a.scan_status='clean'
 AND NOT EXISTS(SELECT 1 FROM product_delivery_roots r WHERE r.asset_id=a.id);
DROP VIEW product_preview_candidates;
