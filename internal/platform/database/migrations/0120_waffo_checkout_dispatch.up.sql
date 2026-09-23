-- The pinned Waffo SDK rotates checkout idempotency keys every minute.
-- A committed dispatch is therefore never permission to send it again.
CREATE VIEW product_waffo_checkout_review AS
 SELECT p.id AS payment_id FROM payment_intents p
 LEFT JOIN product_checkout_requests r ON r.payment_id=p.id
 WHERE p.purpose='product' AND p.provider='waffo_pancake' AND p.status='checkout_pending'
 AND (r.dispatch_protocol IS DISTINCT FROM 'guarded_v1'
   OR EXISTS(SELECT 1 FROM product_checkout_dispatches d WHERE d.payment_id=p.id)
   OR p.provider_checkout_id IS NOT NULL OR p.provider_payment_id IS NOT NULL
   OR p.provider_charge_id IS NOT NULL OR p.checkout_url IS NOT NULL);

CREATE OR REPLACE VIEW product_refund_review AS
 SELECT payment_id FROM product_refund_funds_review
 UNION SELECT payment_id FROM product_refund_dispatch_review
 UNION SELECT payment_id FROM product_webhook_quarantine_review
 UNION SELECT payment_id FROM product_waffo_checkout_review;
