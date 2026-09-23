DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_waffo_checkout_review) THEN
  RAISE EXCEPTION 'cannot remove unresolved Waffo checkout dispatch protection';
 END IF;
END $$;

CREATE OR REPLACE VIEW product_refund_review AS
 SELECT payment_id FROM product_refund_funds_review
 UNION SELECT payment_id FROM product_refund_dispatch_review
 UNION SELECT payment_id FROM product_webhook_quarantine_review;
DROP VIEW product_waffo_checkout_review;
