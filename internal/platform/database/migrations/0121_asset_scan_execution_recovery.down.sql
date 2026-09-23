LOCK TABLE asset_scan_executions IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM asset_scan_executions) OR EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'asset scan execution evidence or running jobs present; rollback refused';
 END IF;
END $$;
DROP TRIGGER asset_scan_execution_job_guard ON jobs;
DROP FUNCTION protect_asset_scan_execution_job();
DROP TRIGGER asset_scan_execution_writer ON assets;
DROP FUNCTION check_asset_scan_execution_writer();
DROP TABLE asset_scan_executions;
DROP FUNCTION protect_asset_scan_execution();
DROP FUNCTION require_asset_scan_execution_protocol();
DELETE FROM maintenance_health WHERE kind='asset_scan_execution_recovery';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery'));
