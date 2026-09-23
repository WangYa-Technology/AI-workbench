DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_checkout_check_dispatches) THEN
  RAISE EXCEPTION 'checkout check reconciliation contains durable evidence; rollback refused';
 END IF;
END $$;
DROP VIEW product_checkout_check_candidates;
DROP TABLE product_checkout_check_dispatches;
DROP FUNCTION check_product_checkout_check_dispatch();
DROP INDEX product_checkout_check_history;
DELETE FROM maintenance_health WHERE kind='product_checkout_reconciliation';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup'));
