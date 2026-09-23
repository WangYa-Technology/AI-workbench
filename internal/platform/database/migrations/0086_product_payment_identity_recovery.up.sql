-- Authenticated recovery is separate from the original outbound request. It
-- grants permission to observe/refund an existing transaction, never to replay
-- an uncertain checkout without its original request.
CREATE TABLE product_payment_identity_recoveries (
 payment_id uuid PRIMARY KEY REFERENCES payment_intents(id),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 identity jsonb NOT NULL CHECK(jsonb_typeof(identity)='object'),
 binding jsonb NOT NULL CHECK(jsonb_typeof(binding)='object'),
 observation jsonb NOT NULL CHECK(jsonb_typeof(observation)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((identity->>'provider'='stripe' AND length(identity->>'merchantId')>0
   AND length(identity->>'endpoint')>0 AND binding->>'paymentId'=payment_id::text
   AND (length(binding->>'providerCheckoutId')>0 OR length(binding->>'providerPaymentId')>0)) IS TRUE)
);
CREATE TRIGGER product_payment_identity_recovery_immutable BEFORE UPDATE OR DELETE ON product_payment_identity_recoveries
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
CREATE UNIQUE INDEX product_payment_identity_recovery_active ON jobs ((payload->>'paymentId'))
 WHERE kind='payment.verify_product_identity' AND status IN ('queued','running');

CREATE VIEW product_payment_identity_gaps AS
SELECT pi.id AS payment_id FROM payment_intents pi WHERE pi.purpose='product'
 AND NOT EXISTS(SELECT 1 FROM product_checkout_requests r WHERE r.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_recoveries r WHERE r.payment_id=pi.id);

CREATE OR REPLACE VIEW product_refund_review AS
SELECT pi.id AS payment_id FROM payment_intents pi
WHERE pi.purpose='product' AND (
 EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
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
