-- One eligibility rule for the owner's selector, preview mutations and public
-- projection. Candidate visibility itself never grants public file access.
CREATE VIEW product_preview_candidates AS
 SELECT a.id AS asset_id,a.owner_id
 FROM assets a JOIN users owner ON owner.id=a.owner_id AND owner.status='active'
 WHERE a.scan_status='clean' AND a.source_type IN ('upload','generation')
 AND a.origin_asset_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM product_delivery_roots r WHERE r.asset_id=a.id);

CREATE OR REPLACE VIEW public_product_previews AS
 SELECT p.id AS product_id,a.id AS asset_id,
 '/api/v1/assets/'||a.id::text||'/content' AS media_url,a.kind,a.width,a.height
 FROM public_products p JOIN assets a ON a.id=p.preview_asset_id
 JOIN product_preview_candidates candidate ON candidate.asset_id=a.id AND candidate.owner_id=p.seller_id;
