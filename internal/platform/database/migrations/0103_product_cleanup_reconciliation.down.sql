DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_cleanup_reconciliations) THEN
  RAISE EXCEPTION 'cannot discard product cleanup reconciliation evidence';
 END IF;
END $$;
DROP TABLE product_cleanup_reconciliations;
DROP FUNCTION guard_product_cleanup_reconciliation();
DROP VIEW product_cleanup_reconciliation_candidates;
DROP VIEW product_cleanup_resolved_orders;
DROP INDEX product_cleanup_reconciliation_scan;
DROP INDEX product_cleanup_reconciliation_last_job;
