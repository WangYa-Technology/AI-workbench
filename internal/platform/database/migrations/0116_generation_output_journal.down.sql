-- Never erase object locations or cleanup obligations to make a downgrade succeed.
LOCK TABLE generation_output_writes IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM generation_output_writes) THEN
  RAISE EXCEPTION 'generation output journal contains durable evidence; rollback refused';
 END IF;
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before generation output rollback';
 END IF;
END $$;
DROP TRIGGER generation_output_result_guard ON generations;
DROP FUNCTION check_generation_output_result();
DROP TRIGGER generation_output_asset_guard ON assets;
DROP FUNCTION protect_generation_output_asset();
DROP TABLE generation_output_writes;
DROP FUNCTION protect_generation_output_write();
DROP TRIGGER generation_output_worker_protocol ON jobs;
DROP FUNCTION check_generation_output_worker_protocol();
DROP TRIGGER generation_output_writer_protocol ON generations;
DROP FUNCTION check_generation_output_protocol();
DROP FUNCTION require_generation_output_protocol();
DELETE FROM maintenance_health WHERE kind='generation_output_cleanup';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation'));
