-- Removing this gate while managed listings exist would publish unreviewed
-- offers and discard seller/reviewer evidence. Roll back only an unused schema.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_publications) OR EXISTS(SELECT 1 FROM product_listing_commands) THEN
  RAISE EXCEPTION 'product publication evidence prevents rollback';
 END IF;
END $$;
CREATE OR REPLACE VIEW public_products AS
 SELECT p.* FROM products p
 JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
 JOIN licenses l ON l.code=p.license_code AND l.status='active'
 JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE p.status='active' AND (a.origin_asset_id IS NULL OR origin.scan_status='clean');
DROP INDEX products_seller_directory;
DROP INDEX product_source_work_references;
DROP INDEX product_source_task_references;
DROP INDEX product_source_task_grants;
DROP INDEX product_source_generation_output;
DROP VIEW product_source_candidates;
DROP VIEW product_listing_versions;
DROP TABLE product_listing_commands;
DROP TABLE product_publications;
