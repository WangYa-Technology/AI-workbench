-- Restore the previous scheduler projection without discarding any receipts.
CREATE OR REPLACE VIEW product_checkout_check_candidates AS
 SELECT pi.id AS payment_id,pi.order_id,pi.version AS payment_version,
 CASE WHEN terminal.observed_at IS NOT NULL THEN LEAST(pi.checkout_expires_at,terminal.observed_at)
 ELSE pi.checkout_expires_at+interval '5 seconds' END AS due_at
 FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
 LEFT JOIN LATERAL (
  SELECT min(l.created_at) AS observed_at FROM product_checkout_lookups l
  WHERE l.payment_id=pi.id AND l.outcome='found' AND l.result->>'outcome'='found'
  AND l.result->'observation'->>'providerCheckoutId'=pi.provider_checkout_id
  AND (l.result->'observation'->>'paymentStatus'='paid' OR l.result->'observation'->>'status'='expired')
  AND isfinite(l.created_at) AND l.created_at<=now()
 ) terminal ON true
 WHERE pi.purpose='product' AND pi.provider='stripe' AND pi.status='checkout_open'
 AND o.status='payment_pending' AND o.buyer_id=pi.payer_id AND o.product_id=pi.resource_id
 AND pi.provider_checkout_id IS NOT NULL AND isfinite(pi.checkout_expires_at)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=pi.id::text)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_check_dispatches d WHERE d.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM payment_provider_events e LEFT JOIN payment_provider_event_processing ep ON ep.event_id=e.id
   WHERE e.payment_id=pi.id AND COALESCE(ep.status,'missing')<>'processed');

DROP VIEW product_checkout_session_evidence;
