-- Scheduling and operator projections share terminal session evidence from
-- both authenticated recovery workflows. This view is eligibility evidence,
-- not authority to grant rights; the worker validates full saved bindings.
CREATE VIEW product_checkout_session_evidence AS
 SELECT e.payment_id,e.job_id,e.source,e.observed_at,
 e.observation->>'providerCheckoutId' AS provider_checkout_id,
 e.observation->>'status' AS status,e.observation->>'paymentStatus' AS payment_status
 FROM (
  SELECT l.payment_id,l.job_id,'lookup'::text AS source,l.created_at AS observed_at,
    l.result->'observation' AS observation
  FROM product_checkout_lookups l WHERE l.outcome='found' AND l.result->>'outcome'='found'
  UNION ALL
  SELECT r.payment_id,r.job_id,'identity_recovery',r.created_at,r.observation->'checkout'
  FROM product_payment_identity_recoveries r
 ) e JOIN payment_intents p ON p.id=e.payment_id
 WHERE p.provider='stripe' AND p.purpose='product'
 AND e.observation->>'providerCheckoutId'=p.provider_checkout_id
 AND e.observation->>'status' IN ('complete','expired')
 AND isfinite(e.observed_at) AND e.observed_at<=now();

CREATE OR REPLACE VIEW product_checkout_check_candidates AS
 SELECT pi.id AS payment_id,pi.order_id,pi.version AS payment_version,
 CASE WHEN terminal.observed_at IS NOT NULL THEN LEAST(pi.checkout_expires_at,terminal.observed_at)
 ELSE pi.checkout_expires_at+interval '5 seconds' END AS due_at
 FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
 LEFT JOIN LATERAL (
  SELECT min(e.observed_at) AS observed_at FROM product_checkout_session_evidence e
  WHERE e.payment_id=pi.id AND (e.payment_status='paid' OR e.status='expired')
 ) terminal ON true
 WHERE pi.purpose='product' AND pi.provider='stripe' AND pi.status='checkout_open'
 AND o.status='payment_pending' AND o.buyer_id=pi.payer_id AND o.product_id=pi.resource_id
 AND pi.provider_checkout_id IS NOT NULL AND isfinite(pi.checkout_expires_at)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=pi.id::text)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_check_dispatches d WHERE d.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM payment_provider_events e LEFT JOIN payment_provider_event_processing ep ON ep.event_id=e.id
   WHERE e.payment_id=pi.id AND COALESCE(ep.status,'missing')<>'processed');
