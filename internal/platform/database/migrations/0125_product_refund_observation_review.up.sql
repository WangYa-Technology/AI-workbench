-- A later failed or incomplete read cannot erase an authenticated refund.
-- Compare immutable observations with actual operation bindings; never invent
-- a local operation or infer that an absent refund was reversed.
CREATE VIEW product_refund_observation_gaps AS
 SELECT c.payment_id,c.id AS check_id,observation->>'providerId' AS provider_refund_id
 FROM product_refund_checks c
 CROSS JOIN LATERAL jsonb_array_elements(COALESCE(c.observations,'[]'::jsonb)) observation
 WHERE c.observed_at IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=c.payment_id AND a.provider='stripe'
   AND a.provider_refund_id=observation->>'providerId'
   AND a.provider_payment_id=observation->>'providerPaymentId'
   AND a.amount_cents::text=observation->>'amountCents' AND a.currency=observation->>'currency'
   AND (observation->>'operationId' IS NULL OR a.operation_id::text=observation->>'operationId')
   AND (observation->>'status'<>'succeeded' OR a.status='succeeded')
 );

CREATE VIEW product_refund_observation_review AS
 SELECT DISTINCT payment_id FROM product_refund_observation_gaps;

CREATE OR REPLACE VIEW product_refund_review AS
 SELECT payment_id FROM product_refund_funds_review
 UNION SELECT payment_id FROM product_refund_dispatch_review
 UNION SELECT payment_id FROM product_webhook_quarantine_review
 UNION SELECT payment_id FROM product_waffo_checkout_review
 UNION SELECT payment_id FROM product_refund_observation_review;

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
