-- Shared subjects and retention rules for cleanup workers and operator recovery.
-- These are derived projections; immutable delivery/recovery evidence is unchanged.
CREATE VIEW product_delivery_cleanup_subjects AS
 SELECT d.order_id,o.buyer_id AS user_id FROM product_delivery_snapshots d JOIN orders o ON o.id=d.order_id
 UNION
 SELECT d.order_id,(c.contract->'product'->>'sellerId')::uuid FROM product_delivery_snapshots d JOIN product_order_contracts c ON c.order_id=d.order_id
 UNION
 SELECT d.order_id,a.owner_id FROM product_delivery_snapshots d JOIN product_order_contracts c ON c.order_id=d.order_id
 JOIN assets a ON a.id IN (c.source_asset_id,c.root_asset_id);

CREATE VIEW product_delivery_cleanup_policy AS
 SELECT d.order_id,d.state,
 (EXISTS(SELECT 1 FROM payment_intents p WHERE p.order_id=d.order_id AND p.status IN ('checkout_pending','checkout_open')) OR
  EXISTS(SELECT 1 FROM entitlements e JOIN users u ON u.id=e.user_id WHERE e.order_id=d.order_id AND e.status='active' AND u.status<>'deleted')) AS needed,
 EXISTS(SELECT 1 FROM data_rights_legal_holds h JOIN product_delivery_cleanup_subjects s ON s.user_id=h.user_id
  WHERE s.order_id=d.order_id AND h.status='active' AND h.expires_at>now()) AS held
 FROM product_delivery_snapshots d;

CREATE INDEX product_delivery_cleanup_jobs_order ON jobs ((payload->>'orderId'),status)
 WHERE kind='product.delivery_cleanup';
