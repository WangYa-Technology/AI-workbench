DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM payment_intents p WHERE p.purpose='product' AND (
  p.status IN ('refund_pending','refund_failed') OR
  EXISTS(SELECT 1 FROM product_refund_review r WHERE r.payment_id=p.id))) THEN
  RAISE EXCEPTION 'cannot remove unresolved product funds media protection';
 END IF;
END $$;

CREATE OR REPLACE VIEW product_delivery_cleanup_policy AS
 SELECT d.order_id,d.state,
 (EXISTS(SELECT 1 FROM payment_intents p WHERE p.order_id=d.order_id AND p.status IN ('checkout_pending','checkout_open')) OR
  EXISTS(SELECT 1 FROM entitlements e JOIN users u ON u.id=e.user_id WHERE e.order_id=d.order_id AND e.status='active' AND u.status<>'deleted') OR
  EXISTS(SELECT 1 FROM payment_intents p WHERE p.order_id=d.order_id AND (
   EXISTS(SELECT 1 FROM product_webhook_quarantine_review q WHERE q.payment_id=p.id) OR
   EXISTS(SELECT 1 FROM product_refund_observation_review r WHERE r.payment_id=p.id)))) AS needed,
 EXISTS(SELECT 1 FROM data_rights_legal_holds h JOIN product_delivery_cleanup_subjects s ON s.user_id=h.user_id
  WHERE s.order_id=d.order_id AND h.status='active' AND h.expires_at>now()) AS held
 FROM product_delivery_snapshots d;

DROP VIEW product_order_funds_retention;
