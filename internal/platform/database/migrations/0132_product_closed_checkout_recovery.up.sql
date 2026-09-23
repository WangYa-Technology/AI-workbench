-- Preserve the closed state and original payment event while historical refunds
-- are checked. No checkout request, fulfilled order or refund outcome is invented.
CREATE TABLE product_closed_checkout_recoveries (
 payment_id uuid PRIMARY KEY REFERENCES payment_intents(id),
 event_id uuid NOT NULL UNIQUE REFERENCES payment_provider_events(id),
 checkout_job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 from_payment_status text NOT NULL CHECK(from_payment_status IN ('cancelled','payment_failed')),
 from_order_status text NOT NULL CHECK(from_order_status IN ('cancelled','payment_failed','payment_pending')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER product_closed_checkout_recovery_immutable BEFORE UPDATE OR DELETE ON product_closed_checkout_recoveries
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
CREATE FUNCTION validate_product_closed_checkout_recovery() RETURNS trigger AS $$
BEGIN
 IF NOT EXISTS(
  SELECT 1 FROM payment_intents p JOIN orders o ON o.id=p.order_id
  JOIN payment_provider_events e ON e.id=NEW.event_id AND e.payment_id=p.id
  JOIN jobs j ON j.id=NEW.checkout_job_id AND j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=p.id::text
  WHERE p.id=NEW.payment_id AND p.provider='stripe' AND p.purpose='product'
   AND p.status=NEW.from_payment_status AND o.status=NEW.from_order_status
   AND o.buyer_id=p.payer_id AND o.product_id=p.resource_id
   AND e.provider=p.provider AND e.purpose=p.purpose AND e.resource_id=p.resource_id
   AND e.object_id=p.provider_checkout_id AND e.checkout_job_id=j.id
   AND e.event_type='checkout.observed' AND e.payment_status='paid' AND e.evidence_source='provider_query'
   AND e.amount_cents=p.amount_cents AND e.currency=p.currency AND e.live_mode=p.live_mode
 ) THEN RAISE EXCEPTION 'closed checkout recovery requires its original paid event and query job'; END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER product_closed_checkout_recovery_binding BEFORE INSERT ON product_closed_checkout_recoveries
 FOR EACH ROW EXECUTE FUNCTION validate_product_closed_checkout_recovery();

CREATE VIEW product_closed_checkout_funds_ready AS
 SELECT r.payment_id,r.event_id FROM product_closed_checkout_recoveries r
 WHERE COALESCE((SELECT c.status='completed' AND c.unresolved_count=0 AND c.observed_at>=r.created_at AND c.created_at>=r.created_at
 AND EXISTS(SELECT 1 FROM product_refund_read_executions x WHERE x.check_id=c.id AND x.complete AND x.started_at>=r.created_at)
 FROM product_refund_checks c WHERE c.payment_id=r.payment_id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_evidence_conflicts g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookups l WHERE l.payment_id=r.payment_id AND l.outcome IN ('ambiguous','state_changed'))
 AND NOT EXISTS(SELECT 1 FROM product_webhook_quarantine_review g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_observation_review g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=r.payment_id AND a.reconciliation_required)
 -- Existing refund obligations and historical rights need their own disposition.
 -- A clean remote read cannot authorize a second compensation for them.
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=r.payment_id AND a.requested_at<=r.created_at)
 AND NOT EXISTS(SELECT 1 FROM payment_intents p JOIN entitlements e ON e.order_id=p.order_id WHERE p.id=r.payment_id);

-- Shared retention needs the unresolved recovery state, not a repeated expansion
-- of every refund query gate. Processing the original event is permitted only
-- after funds_ready; later refund/identity/conflict gaps retain their own gates.
CREATE VIEW product_closed_checkout_recovery_review AS
 SELECT p.id AS payment_id FROM payment_intents p WHERE p.provider='stripe' AND p.purpose='product' AND (
  (p.status IN ('cancelled','payment_failed') AND EXISTS(SELECT 1 FROM product_checkout_paid_observations e WHERE e.payment_id=p.id))
  OR EXISTS(SELECT 1 FROM product_closed_checkout_recoveries r
    LEFT JOIN payment_provider_event_processing e ON e.event_id=r.event_id
    WHERE r.payment_id=p.id AND COALESCE(e.status,'missing')<>'processed')
 );

CREATE VIEW product_closed_checkout_candidates AS
 SELECT p.id AS payment_id,p.order_id,p.version AS payment_version,e.observed_at AS due_at
 FROM payment_intents p JOIN orders o ON o.id=p.order_id
 JOIN LATERAL (SELECT min(s.observed_at) AS observed_at FROM product_checkout_session_evidence s
   WHERE s.payment_id=p.id AND s.payment_status='paid') e ON e.observed_at IS NOT NULL
 WHERE p.provider='stripe' AND p.purpose='product' AND p.status IN ('cancelled','payment_failed')
 AND o.status IN ('cancelled','payment_failed','payment_pending') AND o.buyer_id=p.payer_id AND o.product_id=p.resource_id
 AND p.provider_checkout_id IS NOT NULL AND isfinite(p.checkout_expires_at)
 AND NOT EXISTS(SELECT 1 FROM product_closed_checkout_recoveries r WHERE r.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_evidence_conflicts g WHERE g.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookups l WHERE l.payment_id=p.id AND l.outcome IN ('ambiguous','state_changed'))
 AND NOT EXISTS(SELECT 1 FROM product_webhook_quarantine_review g WHERE g.payment_id=p.id);

CREATE OR REPLACE VIEW product_checkout_lookup_review AS
 SELECT DISTINCT payment_id FROM product_checkout_lookups WHERE outcome IN ('ambiguous','state_changed')
 UNION SELECT payment_id FROM product_checkout_evidence_conflicts
 UNION SELECT payment_id FROM product_closed_checkout_recovery_review;

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
   WHERE e.payment_id=pi.id AND COALESCE(ep.status,'missing')<>'processed')
 UNION ALL
 SELECT c.payment_id,c.order_id,c.payment_version,c.due_at FROM product_closed_checkout_candidates c
 WHERE NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=c.payment_id::text)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_check_dispatches d WHERE d.payment_id=c.payment_id)
 AND NOT EXISTS(SELECT 1 FROM payment_provider_events e LEFT JOIN payment_provider_event_processing ep ON ep.event_id=e.id
 WHERE e.payment_id=c.payment_id AND COALESCE(ep.status,'missing')<>'processed');

CREATE OR REPLACE FUNCTION check_product_checkout_check_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM jobs j JOIN payment_intents p ON p.id=NEW.payment_id
   WHERE j.id=NEW.job_id AND j.kind='payment.check_product_checkout'
   AND j.payload->>'paymentId'=p.id::text AND j.status='queued'
   AND p.purpose='product' AND p.provider='stripe' AND (p.status='checkout_open' OR EXISTS(SELECT 1 FROM product_closed_checkout_candidates c WHERE c.payment_id=p.id))
   AND p.version=NEW.payment_version+1 AND NEW.due_at<=now()) THEN
  RAISE EXCEPTION 'checkout check dispatch requires matching product query job';
 END IF;
 RETURN NEW;
END $$;
