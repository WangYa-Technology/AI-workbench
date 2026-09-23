-- Never erase object locations or cleanup obligations to make a downgrade succeed.
LOCK TABLE upload_writes IN ACCESS EXCLUSIVE MODE;
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM upload_writes) THEN
  RAISE EXCEPTION 'upload write journal contains durable evidence; rollback refused';
 END IF;
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before upload write rollback';
 END IF;
END $$;
DROP TRIGGER upload_write_result_guard ON assets;
DROP FUNCTION check_upload_write_result();
DROP TRIGGER upload_write_asset_guard ON assets;
DROP FUNCTION protect_upload_write_asset();
DROP TABLE upload_writes;
DROP FUNCTION protect_upload_write_evidence();
DROP TRIGGER upload_write_worker_protocol ON jobs;
DROP FUNCTION check_upload_write_worker_protocol();
DROP TRIGGER upload_write_writer_protocol ON assets;
DROP FUNCTION check_upload_write_protocol();
DROP FUNCTION require_upload_write_protocol();
DELETE FROM maintenance_health WHERE kind='upload_write_cleanup';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery','asset_scan_execution_recovery'));
