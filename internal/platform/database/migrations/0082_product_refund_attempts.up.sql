-- Keep each operation after the order advances to a later refund attempt.
CREATE TABLE product_refund_attempts (
  operation_id uuid PRIMARY KEY,
  payment_id uuid NOT NULL REFERENCES payment_intents(id),
  provider text NOT NULL,
  provider_payment_id text NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL,
  correlation_enabled boolean NOT NULL,
  idempotency_key text,
  provider_refund_id text,
  status text NOT NULL CHECK (status IN ('requested','pending','failed','succeeded')),
  reconciliation_required boolean NOT NULL DEFAULT false,
  requested_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (provider_refund_id IS NULL OR length(provider_refund_id)>0)
);
CREATE INDEX product_refund_attempts_payment ON product_refund_attempts(payment_id);
CREATE INDEX product_refund_attempts_reconciliation ON product_refund_attempts(payment_id) WHERE reconciliation_required;
CREATE UNIQUE INDEX product_refund_attempts_provider_identity
  ON product_refund_attempts(payment_id,provider_refund_id) WHERE provider_refund_id IS NOT NULL;

-- Only backfill the operation whose identity is still retained on the order.
-- Do not infer discarded operations from today's mutable order fields.
INSERT INTO product_refund_attempts(operation_id,payment_id,provider,provider_payment_id,
  amount_cents,currency,correlation_enabled,idempotency_key,provider_refund_id,status,requested_at)
SELECT o.refund_operation_id,pi.id,pi.provider,pi.provider_payment_id,pi.amount_cents,pi.currency,
  o.refund_correlation_enabled,o.refund_idempotency_key,COALESCE(pi.provider_refund_id,e.evidence->>'providerRefundId'),
  CASE WHEN pi.status='refunded' THEN 'succeeded'
       WHEN pi.status IN ('paid','refund_failed') THEN 'failed'
       WHEN pi.provider_refund_id IS NOT NULL THEN 'pending' ELSE 'requested' END,
  COALESCE(o.refund_requested_at,o.updated_at)
FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
LEFT JOIN LATERAL (
  SELECT evidence FROM payment_intent_events e WHERE e.payment_id=pi.id
    AND e.event_type='refund.provider_requested' AND e.evidence->>'operationId'=o.refund_operation_id::text
  ORDER BY e.created_at DESC,e.id DESC LIMIT 1
) e ON true
WHERE pi.purpose='product' AND o.refund_operation_id IS NOT NULL AND pi.provider_payment_id IS NOT NULL;

CREATE FUNCTION guard_product_refund_attempt() RETURNS trigger AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'product refund operation evidence cannot be deleted';
  END IF;
  IF ROW(NEW.operation_id,NEW.payment_id,NEW.provider,NEW.provider_payment_id,NEW.amount_cents,NEW.currency,NEW.correlation_enabled,NEW.idempotency_key,NEW.requested_at)
     IS DISTINCT FROM ROW(OLD.operation_id,OLD.payment_id,OLD.provider,OLD.provider_payment_id,OLD.amount_cents,OLD.currency,OLD.correlation_enabled,OLD.idempotency_key,OLD.requested_at)
     OR (OLD.provider_refund_id IS NOT NULL AND NEW.provider_refund_id IS DISTINCT FROM OLD.provider_refund_id)
     OR (OLD.status='succeeded' AND NEW.status<>'succeeded') THEN
    RAISE EXCEPTION 'product refund operation identity and success evidence are immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_refund_attempt_guard BEFORE UPDATE OR DELETE ON product_refund_attempts
  FOR EACH ROW EXECUTE FUNCTION guard_product_refund_attempt();
