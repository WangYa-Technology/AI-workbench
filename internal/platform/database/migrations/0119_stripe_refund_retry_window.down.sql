DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_refund_attempts
   WHERE provider='stripe' AND status IN ('requested','pending') AND provider_refund_id IS NULL) THEN
  RAISE EXCEPTION 'cannot remove unresolved Stripe refund retry protection';
 END IF;
END $$;

CREATE OR REPLACE VIEW product_refund_dispatch_review AS
 SELECT a.payment_id,a.operation_id FROM product_refund_attempts a
 LEFT JOIN product_refund_dispatches d ON d.operation_id=a.operation_id
 WHERE a.provider='waffo_pancake' AND a.status IN ('requested','pending')
 AND (d.operation_id IS NULL OR (d.reserved_at IS NOT NULL AND d.responded_at IS NULL));
