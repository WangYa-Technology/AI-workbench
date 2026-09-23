DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_closed_checkout_recoveries) OR EXISTS(SELECT 1 FROM product_closed_checkout_recovery_review) THEN
  RAISE EXCEPTION 'cannot remove closed checkout recovery evidence or unresolved payment protection';
 END IF;
END $$;
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

CREATE OR REPLACE FUNCTION check_product_checkout_check_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM jobs j JOIN payment_intents p ON p.id=NEW.payment_id
   WHERE j.id=NEW.job_id AND j.kind='payment.check_product_checkout'
   AND j.payload->>'paymentId'=p.id::text AND j.status='queued'
   AND p.purpose='product' AND p.provider='stripe' AND p.status='checkout_open'
   AND p.version=NEW.payment_version+1 AND NEW.due_at<=now()) THEN
  RAISE EXCEPTION 'checkout check dispatch requires matching product query job';
 END IF;
 RETURN NEW;
END $$;

DROP VIEW product_closed_checkout_candidates;
DROP VIEW product_closed_checkout_recovery_review;
DROP VIEW product_closed_checkout_funds_ready;
DROP TABLE product_closed_checkout_recoveries;
DROP FUNCTION validate_product_closed_checkout_recovery();
