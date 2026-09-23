-- A recovered Stripe product session may already be complete/expired and have
-- no hosted URL. Keep it pending verification without inventing a payment link.
ALTER TABLE payment_intents DROP CONSTRAINT payment_intents_check2;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_check2 CHECK (
 status<>'checkout_open' OR (provider_checkout_id IS NOT NULL AND checkout_expires_at IS NOT NULL
 AND (checkout_url IS NOT NULL OR (purpose='product' AND provider='stripe')))
);

CREATE TABLE product_checkout_lookups (
 job_id uuid PRIMARY KEY REFERENCES jobs(id),
 payment_id uuid NOT NULL REFERENCES payment_intents(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 outcome text NOT NULL CHECK(outcome IN ('found','not_found','ambiguous','incomplete','state_changed')),
 searched_after timestamptz NOT NULL,
 searched_before timestamptz NOT NULL CHECK(searched_before>searched_after),
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object'),
 check_job_id uuid REFERENCES jobs(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((result->>'outcome' IN ('found','not_found','ambiguous','incomplete')) IS TRUE),
 CHECK (check_job_id IS NULL OR (outcome='found' AND result->>'outcome'='found'))
);
CREATE TRIGGER product_checkout_lookup_immutable BEFORE UPDATE OR DELETE ON product_checkout_lookups
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
CREATE INDEX product_checkout_lookups_payment ON product_checkout_lookups(payment_id,created_at DESC,job_id);
CREATE UNIQUE INDEX product_checkout_lookup_check ON product_checkout_lookups(check_job_id) WHERE check_job_id IS NOT NULL;
CREATE UNIQUE INDEX product_checkout_lookup_active ON jobs ((payload->>'paymentId'))
 WHERE kind='payment.locate_product_checkout' AND status IN ('queued','running');

CREATE VIEW product_checkout_lookup_review AS SELECT DISTINCT payment_id FROM product_checkout_lookups
 WHERE outcome IN ('ambiguous','state_changed');

CREATE OR REPLACE VIEW product_refund_review AS
SELECT pi.id AS payment_id FROM payment_intents pi
WHERE pi.purpose='product' AND (
 EXISTS(SELECT 1 FROM product_checkout_lookup_review l WHERE l.payment_id=pi.id)
 OR EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
 OR EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=pi.id AND a.reconciliation_required)
 OR COALESCE((SELECT c.status IN ('requested','observed') OR c.unresolved_count>0
   OR (c.status='failed' AND c.observed_at IS NOT NULL)
   FROM product_refund_checks c WHERE c.payment_id=pi.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false)
 -- Recovery establishes merchant ownership, not the absence of past refunds.
 -- Require a completed funds check after recovery, including after failures
 -- with no observation. Unknown/manual refunds continue to hold this gate.
 OR EXISTS(SELECT 1 FROM product_payment_identity_recoveries r WHERE r.payment_id=pi.id
   AND pi.provider_payment_id IS NOT NULL AND pi.status IN ('paid','refund_pending','refund_failed','refunded')
   AND NOT COALESCE((SELECT c.status='completed' AND c.unresolved_count=0 AND c.observed_at>=r.created_at
     FROM product_refund_checks c WHERE c.payment_id=pi.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false))
);
