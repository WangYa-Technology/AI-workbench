-- All cleanup entry points must retain delivery evidence while a refund is
-- pending, compensation failed, or any shared financial review remains open.
-- Revoking a deleted buyer's access does not settle their original payment.
CREATE VIEW product_order_funds_retention AS
 SELECT DISTINCT p.order_id FROM payment_intents p
 WHERE p.purpose='product' AND p.order_id IS NOT NULL AND (
  p.status IN ('checkout_pending','checkout_open','refund_pending','refund_failed') OR
  EXISTS(SELECT 1 FROM product_refund_review r WHERE r.payment_id=p.id)
 );

CREATE OR REPLACE VIEW product_delivery_cleanup_policy AS
 SELECT d.order_id,d.state,
 (EXISTS(SELECT 1 FROM product_order_funds_retention f WHERE f.order_id=d.order_id) OR
  EXISTS(SELECT 1 FROM entitlements e JOIN users u ON u.id=e.user_id
   WHERE e.order_id=d.order_id AND e.status='active' AND u.status<>'deleted')) AS needed,
 EXISTS(SELECT 1 FROM data_rights_legal_holds h JOIN product_delivery_cleanup_subjects s ON s.user_id=h.user_id
  WHERE s.order_id=d.order_id AND h.status='active' AND h.expires_at>now()) AS held
 FROM product_delivery_snapshots d;
