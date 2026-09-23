-- Each observation was authenticated and validated by its original writer.
-- This projection detects disagreement; it never grants financial authority.
CREATE VIEW product_checkout_paid_observations AS
 SELECT l.payment_id,'lookup'::text AS source,l.job_id AS source_id,
 l.result->'observation' AS observation
 FROM product_checkout_lookups l
 WHERE l.outcome='found' AND l.result->>'outcome'='found'
 AND l.result->'observation'->>'paymentStatus'='paid'
 UNION ALL
 SELECT r.payment_id,'identity_recovery',r.job_id,r.observation->'checkout'
 FROM product_payment_identity_recoveries r
 WHERE r.observation->'checkout'->>'paymentStatus'='paid'
 UNION ALL
 SELECT e.payment_id,'checkout_query',e.id,e.evidence->'observation'
 FROM payment_intent_events e JOIN jobs j ON j.id::text=e.evidence->>'jobId'
 AND j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=e.payment_id::text
 WHERE e.event_type='checkout.queried' AND e.evidence->>'source'='provider_query'
 AND e.evidence->'observation'->>'paymentStatus'='paid';

CREATE VIEW product_checkout_evidence_conflicts AS
 SELECT o.payment_id FROM product_checkout_paid_observations o
 JOIN payment_intents p ON p.id=o.payment_id AND p.purpose='product' AND p.provider='stripe'
 GROUP BY o.payment_id HAVING count(DISTINCT(o.observation-'expiresAt'))>1;

-- Existing financial review, media retention and finance attention all depend
-- on this projection. Keep those consumers aligned with checkout fulfillment.
CREATE OR REPLACE VIEW product_checkout_lookup_review AS
 SELECT DISTINCT payment_id FROM product_checkout_lookups WHERE outcome IN ('ambiguous','state_changed')
 UNION SELECT payment_id FROM product_checkout_evidence_conflicts;

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
 AND NOT EXISTS(SELECT 1 FROM product_checkout_evidence_conflicts c WHERE c.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=pi.id::text)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_check_dispatches d WHERE d.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM payment_provider_events e LEFT JOIN payment_provider_event_processing ep ON ep.event_id=e.id
   WHERE e.payment_id=pi.id AND COALESCE(ep.status,'missing')<>'processed');
