-- Automatic checks are system reads, never attributed to a fabricated user.
ALTER TABLE product_refund_checks ALTER COLUMN requested_by DROP NOT NULL;
ALTER TABLE product_refund_checks ADD COLUMN origin text NOT NULL DEFAULT 'operator'
 CHECK(origin IN ('operator','automatic'));
ALTER TABLE product_refund_checks ADD CONSTRAINT product_refund_check_actor
 CHECK((origin='operator' AND requested_by IS NOT NULL) OR (origin='automatic' AND requested_by IS NULL));

CREATE OR REPLACE FUNCTION protect_refund_check_evidence() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.status IN ('completed','failed') OR
   ROW(NEW.id,NEW.payment_id,NEW.requested_by,NEW.job_id,NEW.created_at,NEW.origin) IS DISTINCT FROM
   ROW(OLD.id,OLD.payment_id,OLD.requested_by,OLD.job_id,OLD.created_at,OLD.origin) OR
   (OLD.observed_at IS NOT NULL AND ROW(NEW.observations,NEW.observed_at) IS DISTINCT FROM ROW(OLD.observations,OLD.observed_at)) THEN
   RAISE EXCEPTION 'refund query evidence is immutable';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE INDEX product_refund_automatic_checks ON product_refund_checks(payment_id) WHERE origin='automatic';
CREATE INDEX product_refund_unresolved_scan ON product_refund_attempts(payment_id,requested_at)
 WHERE status IN ('requested','pending') OR reconciliation_required;
CREATE INDEX product_refund_dispatch_active ON jobs ((payload->>'paymentId'))
 WHERE kind='payment.refund_product' AND status IN ('queued','running');

-- Eligibility is rechecked while holding the payment row. The first read is
-- due after 15 minutes. Further automatic reads back off to at most daily.
-- Failed/cancelled query jobs require explicit operator recovery; do not reset
-- their attempts or discard observations. Active dispatch is left to its job.
CREATE VIEW product_refund_reconciliation_candidates AS
 SELECT pi.id AS payment_id,
 COALESCE(last_check.completed_at,last_check.created_at,attempt.requested_at)
   + make_interval(mins => LEAST(1440,15 * (1 << auto_count.n::int))) AS due_at
 FROM payment_intents pi
 JOIN (SELECT a.payment_id,min(a.requested_at) AS requested_at FROM product_refund_attempts a
   WHERE a.status IN ('requested','pending') OR a.reconciliation_required GROUP BY a.payment_id) attempt ON attempt.payment_id=pi.id
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
