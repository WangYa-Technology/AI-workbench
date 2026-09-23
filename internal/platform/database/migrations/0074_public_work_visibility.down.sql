DROP INDEX generations_public_lineage;
CREATE OR REPLACE VIEW community_visible_posts AS
 SELECT p.* FROM posts p JOIN users u ON u.id=p.author_id
 LEFT JOIN works w ON w.id=p.work_id LEFT JOIN assets a ON a.id=w.asset_id
 WHERE p.status='published' AND NOT p.owner_removed AND u.status='active'
 AND (p.work_id IS NULL OR (w.status='published' AND a.scan_status='clean'));
DROP VIEW public_works;
