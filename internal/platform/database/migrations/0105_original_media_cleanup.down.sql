DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM original_media_cleanup_receipts) OR EXISTS(SELECT 1 FROM original_media_cleanup_reconciliations) THEN
  RAISE EXCEPTION 'cannot discard original media cleanup evidence';
 END IF;
END $$;
DROP TABLE original_media_cleanup_reconciliations;
DROP FUNCTION guard_original_media_cleanup_reconciliation();
DROP VIEW original_media_cleanup_candidates;
DROP VIEW original_media_cleanup_policy;
DROP TABLE original_media_cleanup_receipts;
DROP FUNCTION guard_original_media_cleanup_receipt();
DROP VIEW original_media_cleanup_locations;
DROP INDEX original_media_cleanup_last_job;
DROP INDEX original_media_cleanup_completed_scan;
