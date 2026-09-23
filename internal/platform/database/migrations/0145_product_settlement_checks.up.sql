CREATE TABLE product_settlement_checks (
 id uuid PRIMARY KEY,
 settlement_id uuid NOT NULL REFERENCES product_settlements(id),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 outcome text CHECK(outcome IN ('found','not_found','ambiguous','incomplete','error','skipped','reversed')),
 error_code text NOT NULL DEFAULT '',
 evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 finished_at timestamptz,
 CHECK ((outcome IS NULL) = (finished_at IS NULL))
);
CREATE INDEX product_settlement_checks_history_idx ON product_settlement_checks(settlement_id,created_at DESC,id DESC);

CREATE FUNCTION protect_product_settlement_check() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'product settlement checks are immutable'; END IF;
 IF OLD.finished_at IS NOT NULL OR NEW.finished_at IS NULL OR
 ROW(NEW.id,NEW.settlement_id,NEW.job_id,NEW.created_at) IS DISTINCT FROM
 ROW(OLD.id,OLD.settlement_id,OLD.job_id,OLD.created_at)
 THEN RAISE EXCEPTION 'product settlement check evidence is immutable'; END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER product_settlement_checks_guard BEFORE UPDATE OR DELETE ON product_settlement_checks
 FOR EACH ROW EXECUTE FUNCTION protect_product_settlement_check();

-- Scanning an empty remote list cannot prove that an uncertain transfer failed.
-- New read jobs retain previous failures and back off without resetting them.
CREATE VIEW product_settlement_check_candidates AS
 SELECT ps.id AS settlement_id,ps.payment_id,
 GREATEST(d.reserved_at+interval '30 seconds',
   COALESCE(last_check.finished_at,last_job.updated_at,last_check.created_at)+interval '5 minutes') AS due_at
 FROM product_settlements ps
 JOIN product_settlement_dispatches d ON d.settlement_id=ps.id AND d.reserved_at IS NOT NULL
 LEFT JOIN LATERAL (
   SELECT c.* FROM product_settlement_checks c WHERE c.settlement_id=ps.id
   ORDER BY c.created_at DESC,c.id DESC LIMIT 1
 ) last_check ON true
 LEFT JOIN jobs last_job ON last_job.id=last_check.job_id
 WHERE ps.provider='stripe' AND ps.provider_transfer_id IS NULL
 AND ps.status IN ('transfer_pending','recovery_required')
 AND ps.payout_batch_id IS NOT NULL AND ps.destination_id IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM product_settlement_checks c JOIN jobs j ON j.id=c.job_id
   WHERE c.settlement_id=ps.id AND j.status IN ('queued','running'));

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation','product_settlement_reconciliation'));
