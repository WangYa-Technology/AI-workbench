-- One public work boundary for browsing, media access, community and attribution.
CREATE VIEW public_works AS
 SELECT w.* FROM works w
 JOIN users u ON u.id=w.author_id AND u.status='active'
 JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE w.status='published' AND (origin.id IS NULL OR origin.scan_status='clean');

CREATE OR REPLACE VIEW community_visible_posts AS
 SELECT p.* FROM posts p JOIN users u ON u.id=p.author_id
 WHERE p.status='published' AND NOT p.owner_removed AND u.status='active'
 AND (p.work_id IS NULL OR EXISTS(SELECT 1 FROM public_works w WHERE w.id=p.work_id));

CREATE INDEX generations_public_lineage ON generations(output_asset_id) WHERE status='succeeded' AND source_work_id IS NOT NULL;
