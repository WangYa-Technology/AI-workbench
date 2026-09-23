-- Waffo product checkout lookup evidence is a read-only payment observation.
-- It is accepted only when the lookup job, immutable request, and original
-- order agree. The query can never create a new checkout or silently replace
-- a signed webhook.
ALTER TABLE payment_provider_events DROP CONSTRAINT payment_query_evidence;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_query_evidence CHECK ((
  (evidence_source='webhook' AND refund_check_id IS NULL AND checkout_job_id IS NULL) OR
  (evidence_source='provider_query' AND purpose='product' AND (
    (event_type='refund.observed' AND refund_check_id IS NOT NULL AND checkout_job_id IS NULL AND provider='stripe') OR
    (event_type='checkout.observed' AND checkout_job_id IS NOT NULL AND refund_check_id IS NULL
      AND payment_status='paid' AND provider_payment_id IS NOT NULL
      AND ((provider='stripe' AND provider_charge_id IS NOT NULL)
        OR (provider='waffo_pancake' AND provider_charge_id IS NULL
          AND api_version='waffo-product-checkout-lookup-v1'
          AND object_type='checkout.order')))
  ))
) IS TRUE);

-- A Waffo lookup may recover a terminal paid order without a hosted URL. The
-- deferred trigger below requires the immutable lookup receipt before commit.
ALTER TABLE payment_intents DROP CONSTRAINT payment_intents_check2;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_check2 CHECK (
  status<>'checkout_open' OR (provider_checkout_id IS NOT NULL AND checkout_expires_at IS NOT NULL
    AND (checkout_url IS NOT NULL OR (purpose='product' AND provider IN ('stripe','waffo_pancake'))))
);

CREATE FUNCTION validate_waffo_lookup_open() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status='checkout_open' AND NEW.purpose='product' AND NEW.provider='waffo_pancake'
     AND NEW.checkout_url IS NULL THEN
    IF NOT EXISTS (
      SELECT 1
      FROM product_checkout_lookups l
      JOIN jobs lj ON lj.id=l.job_id
        AND lj.kind='payment.locate_product_checkout'
        AND lj.payload->>'paymentId'=NEW.id::text
      JOIN product_checkout_requests r ON r.payment_id=NEW.id
        AND r.identity->>'provider'='waffo_pancake'
        AND r.request->>'PaymentID'=NEW.id::text
        AND r.request->>'Purpose'='product'
      JOIN orders o ON o.id=NEW.order_id
        AND o.buyer_id=NEW.payer_id AND o.product_id=NEW.resource_id
      WHERE l.payment_id=NEW.id AND l.outcome='found'
        AND l.result->>'outcome'='found'
        AND l.result->'observation'->>'providerCheckoutId'=NEW.provider_checkout_id
        AND l.result->'observation'->>'paymentStatus'='paid'
        AND l.result->'observation'->>'providerPaymentId' IS NOT NULL
        AND l.result->'observation'->>'amountCents'=NEW.amount_cents::text
        AND upper(l.result->'observation'->>'currency')=upper(NEW.currency)
        AND (l.result->'observation'->>'liveMode')::boolean=NEW.live_mode
        AND l.check_job_id IS NOT NULL
    ) THEN
      RAISE EXCEPTION 'Waffo checkout without URL requires an immutable paid lookup receipt';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE CONSTRAINT TRIGGER payment_intents_waffo_lookup_open_guard
AFTER INSERT OR UPDATE ON payment_intents
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_waffo_lookup_open();

-- Shared projections must include the provider-neutral lookup receipt. They
-- remain projections only; fulfillment performs the complete binding checks.
CREATE OR REPLACE VIEW product_checkout_session_evidence AS
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
 WHERE p.provider IN ('stripe','waffo_pancake') AND p.purpose='product'
 AND e.observation->>'providerCheckoutId'=p.provider_checkout_id
 AND e.observation->>'status' IN ('complete','expired')
 AND isfinite(e.observed_at) AND e.observed_at<=now();

CREATE OR REPLACE VIEW product_checkout_paid_observations AS
 SELECT l.payment_id,'lookup'::text AS source,l.job_id AS source_id,
 l.result->'observation' AS observation
 FROM product_checkout_lookups l
 JOIN payment_intents p ON p.id=l.payment_id AND p.provider IN ('stripe','waffo_pancake') AND p.purpose='product'
 WHERE l.outcome='found' AND l.result->>'outcome'='found'
 AND l.result->'observation'->>'paymentStatus'='paid'
 UNION ALL
 SELECT r.payment_id,'identity_recovery',r.job_id,r.observation->'checkout'
 FROM product_payment_identity_recoveries r
 JOIN payment_intents p ON p.id=r.payment_id AND p.provider='stripe' AND p.purpose='product'
 WHERE r.observation->'checkout'->>'paymentStatus'='paid'
 UNION ALL
 SELECT e.payment_id,'checkout_query',e.id,e.evidence->'observation'
 FROM payment_intent_events e JOIN jobs j ON j.id::text=e.evidence->>'jobId'
 JOIN payment_intents p ON p.id=e.payment_id AND p.provider IN ('stripe','waffo_pancake') AND p.purpose='product'
 AND j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=e.payment_id::text
 WHERE e.event_type='checkout.queried' AND e.evidence->>'source'='provider_query'
 AND e.evidence->'observation'->>'paymentStatus'='paid';

CREATE OR REPLACE VIEW product_checkout_evidence_conflicts AS
 SELECT o.payment_id FROM product_checkout_paid_observations o
 GROUP BY o.payment_id HAVING count(DISTINCT(o.observation-'expiresAt'))>1;

CREATE OR REPLACE VIEW product_checkout_check_candidates AS
 SELECT pi.id AS payment_id,pi.order_id,pi.version AS payment_version,
 CASE WHEN terminal.observed_at IS NOT NULL THEN LEAST(pi.checkout_expires_at,terminal.observed_at)
 ELSE pi.checkout_expires_at+interval '5 seconds' END AS due_at
 FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
 LEFT JOIN LATERAL (
  SELECT min(e.observed_at) AS observed_at FROM product_checkout_session_evidence e
  WHERE e.payment_id=pi.id AND (e.payment_status='paid' OR e.status='expired')
 ) terminal ON true
 WHERE pi.purpose='product' AND pi.provider IN ('stripe','waffo_pancake') AND pi.status='checkout_open'
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
   AND p.purpose='product' AND p.provider IN ('stripe','waffo_pancake')
   AND (p.status='checkout_open' OR EXISTS(SELECT 1 FROM product_closed_checkout_candidates c WHERE c.payment_id=p.id))
   AND p.version=NEW.payment_version+1 AND NEW.due_at<=now()) THEN
  RAISE EXCEPTION 'checkout check dispatch requires matching product query job';
 END IF;
 RETURN NEW;
END $$;
