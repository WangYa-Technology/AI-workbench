-- A late authenticated response is evidence, not permission to overwrite the
-- primary check or replay a financial operation.
CREATE TABLE product_refund_read_receipts (
 id uuid PRIMARY KEY,
 check_id uuid NOT NULL REFERENCES product_refund_checks(id),
 attempt_number integer NOT NULL CHECK(attempt_number>=0),
 lease_token uuid REFERENCES job_attempts(lease_token),
 complete boolean NOT NULL,
 error_code text,
 observations jsonb NOT NULL CHECK(jsonb_typeof(observations)='array' AND jsonb_array_length(observations) BETWEEN 1 AND 1000),
 evidence_sha256 text NOT NULL CHECK(evidence_sha256 ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((attempt_number=0 AND lease_token IS NULL) OR (attempt_number>0 AND lease_token IS NOT NULL)),
 CHECK((complete AND error_code IS NULL) OR (NOT complete AND error_code IS NOT NULL)),
 UNIQUE(check_id,attempt_number,evidence_sha256)
);
CREATE INDEX product_refund_read_receipts_history ON product_refund_read_receipts(check_id,created_at DESC,id DESC);
CREATE FUNCTION protect_refund_read_receipt() RETURNS trigger AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'refund read receipts are immutable'; END IF;
 IF NEW.lease_token IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM product_refund_checks c JOIN job_attempts a ON a.job_id=c.job_id
  WHERE c.id=NEW.check_id AND a.lease_token=NEW.lease_token AND a.attempt_number=NEW.attempt_number
 ) THEN RAISE EXCEPTION 'refund receipt execution mismatch'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_refund_read_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON product_refund_read_receipts
 FOR EACH ROW EXECUTE FUNCTION protect_refund_read_receipt();

CREATE OR REPLACE VIEW product_refund_observation_gaps AS
 SELECT c.payment_id,c.id AS check_id,observation->>'providerId' AS provider_refund_id
 FROM product_refund_checks c
 CROSS JOIN LATERAL (
  SELECT value AS observation FROM jsonb_array_elements(COALESCE(c.observations,'[]'::jsonb)) WHERE c.observed_at IS NOT NULL
  UNION ALL
  SELECT value FROM product_refund_read_receipts r CROSS JOIN LATERAL jsonb_array_elements(r.observations)
  WHERE r.check_id=c.id
 ) evidence
 WHERE NOT EXISTS (
  SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=c.payment_id AND a.provider='stripe'
   AND a.provider_refund_id=observation->>'providerId'
   AND a.provider_payment_id=observation->>'providerPaymentId'
   AND a.amount_cents::text=observation->>'amountCents' AND a.currency=observation->>'currency'
   AND (observation->>'operationId' IS NULL OR a.operation_id::text=observation->>'operationId')
   AND (observation->>'status'<>'succeeded' OR a.status='succeeded')
 );
