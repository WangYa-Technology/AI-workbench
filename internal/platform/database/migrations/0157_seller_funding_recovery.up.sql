CREATE TABLE seller_payout_funding_checks (
 job_id uuid PRIMARY KEY REFERENCES jobs(id),
 transfer_id uuid NOT NULL REFERENCES seller_payout_funding_dispatches(transfer_id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX seller_payout_funding_checks_history_idx
 ON seller_payout_funding_checks(transfer_id,created_at DESC,job_id DESC);

CREATE FUNCTION protect_seller_payout_funding_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'seller funding recovery binding is immutable' USING ERRCODE='55000';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM seller_payout_funding_dispatches d JOIN jobs j ON j.id=NEW.job_id
  WHERE d.transfer_id=NEW.transfer_id AND d.started_at IS NOT NULL
   AND j.kind='payment.check_seller_payout_funding'
   AND j.payload=jsonb_build_object('transferId',d.transfer_id) AND j.status='queued'
 ) THEN
  RAISE EXCEPTION 'seller funding recovery requires original dispatch evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_funding_check_guard
 BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_funding_checks
 FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_funding_check();

-- Stopped writes are never requeued. Append read-only jobs, retaining every
-- original attempt and spacing checks from the last worker/observation result.
CREATE VIEW seller_payout_funding_check_candidates AS
 SELECT d.transfer_id,d.payment_id,
 GREATEST(d.started_at,j.updated_at,last_check.created_at,last_job.updated_at,last_read.finished_at)
  +interval '5 minutes' AS due_at
 FROM seller_payout_funding_dispatches d
 JOIN seller_payout_transfers t ON t.id=d.transfer_id
 JOIN jobs j ON j.id=d.job_id
 LEFT JOIN LATERAL (
  SELECT c.* FROM seller_payout_funding_checks c WHERE c.transfer_id=d.transfer_id
  ORDER BY c.created_at DESC,c.job_id DESC LIMIT 1
 ) last_check ON true
 LEFT JOIN jobs last_job ON last_job.id=last_check.job_id
 LEFT JOIN LATERAL (
  SELECT max(r.finished_at) AS finished_at FROM seller_payout_funding_reads r WHERE r.transfer_id=d.transfer_id
 ) last_read ON true
 WHERE d.started_at IS NOT NULL AND t.status IN ('processing','reconciliation_required')
 AND j.status IN ('failed','cancelled','succeeded')
 AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_checks c JOIN jobs active ON active.id=c.job_id
  WHERE c.transfer_id=d.transfer_id AND active.status IN ('queued','running'));

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation','product_settlement_reconciliation',
 'seller_funding_reconciliation'));
