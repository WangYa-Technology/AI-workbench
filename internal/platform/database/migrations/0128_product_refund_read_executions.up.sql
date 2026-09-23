-- Register each remote read before dispatch. A lost process must not turn a
-- replacement's result into proof that the original read never happened.
CREATE TABLE product_refund_read_executions (
 id uuid PRIMARY KEY,
 check_id uuid NOT NULL REFERENCES product_refund_checks(id),
 attempt_number integer NOT NULL CHECK(attempt_number>=0),
 lease_token uuid REFERENCES job_attempts(lease_token),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 read_deadline timestamptz NOT NULL DEFAULT (clock_timestamp()+interval '20 seconds'),
 recorded_at timestamptz CHECK(recorded_at IS NULL OR isfinite(recorded_at)),
 complete boolean,
 error_code text,
 CHECK(isfinite(started_at) AND isfinite(read_deadline) AND read_deadline>started_at AND read_deadline<=started_at+interval '21 seconds'),
 CHECK((attempt_number=0 AND lease_token IS NULL) OR (attempt_number>0 AND lease_token IS NOT NULL)),
 CHECK((recorded_at IS NULL AND complete IS NULL AND error_code IS NULL) OR
       (recorded_at IS NOT NULL AND complete IS NOT NULL AND
        ((complete AND error_code IS NULL) OR (NOT complete AND error_code IS NOT NULL))))
);
CREATE INDEX product_refund_read_executions_check ON product_refund_read_executions(check_id,started_at,id);
CREATE INDEX product_refund_read_executions_missing ON product_refund_read_executions(read_deadline,check_id) WHERE recorded_at IS NULL;
CREATE FUNCTION protect_product_refund_read_execution() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'refund read executions are immutable'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.recorded_at IS NOT NULL THEN RAISE EXCEPTION 'refund read must be registered before recording'; END IF;
  IF NEW.lease_token IS NOT NULL AND NOT EXISTS(
   SELECT 1 FROM product_refund_checks c JOIN job_attempts a ON a.job_id=c.job_id
   WHERE c.id=NEW.check_id AND a.lease_token=NEW.lease_token AND a.attempt_number=NEW.attempt_number
  ) THEN RAISE EXCEPTION 'refund read execution mismatch'; END IF;
 ELSE
  IF OLD.recorded_at IS NOT NULL OR NEW.recorded_at IS NULL OR
   ROW(NEW.id,NEW.check_id,NEW.attempt_number,NEW.lease_token,NEW.started_at,NEW.read_deadline) IS DISTINCT FROM
   ROW(OLD.id,OLD.check_id,OLD.attempt_number,OLD.lease_token,OLD.started_at,OLD.read_deadline)
  THEN RAISE EXCEPTION 'refund read execution evidence is immutable'; END IF;
 END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER product_refund_read_execution_guard BEFORE INSERT OR UPDATE OR DELETE ON product_refund_read_executions
 FOR EACH ROW EXECUTE FUNCTION protect_product_refund_read_execution();

CREATE TABLE product_refund_read_recoveries (
 execution_id uuid PRIMARY KEY REFERENCES product_refund_read_executions(id),
 recovery_execution_id uuid NOT NULL REFERENCES product_refund_read_executions(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(execution_id<>recovery_execution_id)
);
CREATE FUNCTION protect_product_refund_read_recovery() RETURNS trigger AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'refund read recovery is immutable'; END IF;
 IF NOT EXISTS(
  SELECT 1 FROM product_refund_read_executions prior_read
  JOIN product_refund_checks oc ON oc.id=prior_read.check_id
  JOIN jobs j ON j.id=oc.job_id
  JOIN product_refund_read_executions fresh ON fresh.id=NEW.recovery_execution_id
  JOIN product_refund_checks fc ON fc.id=fresh.check_id AND fc.payment_id=oc.payment_id
  WHERE prior_read.id=NEW.execution_id AND prior_read.recorded_at IS NULL
   AND fresh.recorded_at IS NOT NULL AND fresh.complete
   AND fresh.started_at>prior_read.read_deadline+interval '5 seconds'
   AND NOT(j.status='running' AND j.lease_token IS NOT DISTINCT FROM prior_read.lease_token AND j.lease_expires_at>clock_timestamp())
 ) THEN RAISE EXCEPTION 'refund read recovery lacks a subsequent complete query'; END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER product_refund_read_recovery_guard BEFORE INSERT OR UPDATE OR DELETE ON product_refund_read_recoveries
 FOR EACH ROW EXECUTE FUNCTION protect_product_refund_read_recovery();

CREATE VIEW product_refund_read_gaps AS
 SELECT c.payment_id,c.id AS check_id,e.id AS execution_id,e.started_at,e.read_deadline
 FROM product_refund_read_executions e JOIN product_refund_checks c ON c.id=e.check_id
 WHERE e.recorded_at IS NULL AND NOT EXISTS(SELECT 1 FROM product_refund_read_recoveries r WHERE r.execution_id=e.id);
CREATE OR REPLACE VIEW product_refund_review AS
 SELECT payment_id FROM product_refund_funds_review
 UNION SELECT payment_id FROM product_refund_dispatch_review
 UNION SELECT payment_id FROM product_webhook_quarantine_review
 UNION SELECT payment_id FROM product_waffo_checkout_review
 UNION SELECT payment_id FROM product_refund_observation_review
 UNION SELECT payment_id FROM product_refund_read_gaps;

CREATE OR REPLACE VIEW product_refund_reconciliation_candidates AS
 SELECT pi.id AS payment_id,
 COALESCE(last_check.completed_at,last_check.created_at,attempt.requested_at)
   + make_interval(mins => LEAST(1440,15 * (1 << auto_count.n::int))) AS due_at
 FROM payment_intents pi
 JOIN (SELECT evidence.payment_id,min(evidence.since) AS requested_at FROM (
 SELECT a.payment_id,a.requested_at AS since FROM product_refund_attempts a WHERE a.status IN ('requested','pending') OR a.reconciliation_required
 UNION ALL SELECT g.payment_id,g.read_deadline+interval '5 seconds' FROM product_refund_read_gaps g
 ) evidence GROUP BY evidence.payment_id) attempt ON attempt.payment_id=pi.id
 LEFT JOIN LATERAL (SELECT c.status,c.created_at,c.completed_at,j.status AS job_status
   FROM product_refund_checks c JOIN jobs j ON j.id=c.job_id WHERE c.payment_id=pi.id
   ORDER BY c.created_at DESC,c.id DESC LIMIT 1) last_check ON true
 CROSS JOIN LATERAL (SELECT count(*) AS n FROM (SELECT 1 FROM product_refund_checks c
   WHERE c.payment_id=pi.id AND c.origin='automatic' LIMIT 7) bounded) auto_count
 WHERE pi.purpose='product' AND pi.provider='stripe' AND pi.provider_payment_id IS NOT NULL
 AND pi.status IN ('paid','refund_pending','refund_failed','refunded')
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
 AND (last_check.status IS NULL OR (last_check.status='completed' AND last_check.job_status='succeeded'))
 AND NOT EXISTS(SELECT 1 FROM product_refund_checks c WHERE c.payment_id=pi.id AND c.status IN ('requested','observed'))
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='payment.refund_product'
   AND j.payload->>'paymentId'=pi.id::text AND j.status IN ('queued','running'));
