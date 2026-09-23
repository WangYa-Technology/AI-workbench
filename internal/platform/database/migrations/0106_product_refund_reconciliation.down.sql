DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_refund_checks WHERE origin='automatic') THEN
  RAISE EXCEPTION 'cannot discard automatic refund query provenance';
 END IF;
END $$;
DROP VIEW product_refund_reconciliation_candidates;
DROP INDEX product_refund_dispatch_active;
DROP INDEX product_refund_automatic_checks;
DROP INDEX product_refund_unresolved_scan;
CREATE OR REPLACE FUNCTION protect_refund_check_evidence() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.status IN ('completed','failed') OR
   ROW(NEW.id,NEW.payment_id,NEW.requested_by,NEW.job_id,NEW.created_at) IS DISTINCT FROM
   ROW(OLD.id,OLD.payment_id,OLD.requested_by,OLD.job_id,OLD.created_at) OR
   (OLD.observed_at IS NOT NULL AND ROW(NEW.observations,NEW.observed_at) IS DISTINCT FROM ROW(OLD.observations,OLD.observed_at)) THEN
   RAISE EXCEPTION 'refund query evidence is immutable';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
ALTER TABLE product_refund_checks DROP CONSTRAINT product_refund_check_actor;
ALTER TABLE product_refund_checks DROP COLUMN origin;
ALTER TABLE product_refund_checks ALTER COLUMN requested_by SET NOT NULL;
