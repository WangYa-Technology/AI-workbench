-- Do not silently stop automatic follow-up for unresolved historical refunds.
DO $$ BEGIN
 IF EXISTS(
  SELECT 1 FROM product_closed_checkout_recoveries r JOIN payment_intents p ON p.id=r.payment_id
  WHERE p.status IN ('cancelled','payment_failed') AND (
   EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=p.id AND (a.status IN ('requested','pending') OR a.reconciliation_required))
   OR EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.payment_id=p.id))
 ) THEN RAISE EXCEPTION 'cannot remove automatic closed refund follow-up while funds remain unresolved'; END IF;
END $$;
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
