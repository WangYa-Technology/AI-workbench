-- Stripe can prune an idempotency key after 24 hours. Use the immutable
-- operation request time and a conservative 23-hour window, not job age or a
-- mutable order update timestamp. An uncertain old request requires a read.
CREATE OR REPLACE VIEW product_refund_dispatch_review AS
 SELECT a.payment_id,a.operation_id FROM product_refund_attempts a
 LEFT JOIN product_refund_dispatches d ON d.operation_id=a.operation_id
 WHERE a.provider='waffo_pancake' AND a.status IN ('requested','pending')
 AND (d.operation_id IS NULL OR (d.reserved_at IS NOT NULL AND d.responded_at IS NULL))
 UNION ALL
 SELECT a.payment_id,a.operation_id FROM product_refund_attempts a
 JOIN payment_intents pi ON pi.id=a.payment_id
 JOIN orders o ON o.id=pi.order_id
 WHERE a.provider='stripe' AND a.status IN ('requested','pending') AND a.provider_refund_id IS NULL
 AND (a.requested_at>clock_timestamp() OR a.requested_at<=clock_timestamp()-interval '23 hours'
   OR (o.refund_operation_id=a.operation_id AND
     (o.refund_requested_at IS NULL OR o.refund_requested_at IS DISTINCT FROM a.requested_at
       OR o.refund_correlation_enabled IS DISTINCT FROM a.correlation_enabled)));
