LOCK TABLE seller_source_reversal_dispatches IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_source_reversal_dispatches) THEN
  RAISE EXCEPTION 'source reversal execution evidence must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
DROP VIEW seller_source_reversal_check_candidates;
DROP TABLE seller_source_reversal_checks;
DROP FUNCTION protect_seller_source_reversal_check();
DROP TABLE seller_source_reversal_results;
DROP FUNCTION protect_seller_source_reversal_result();
DROP TABLE seller_source_reversal_reads;
DROP FUNCTION protect_seller_source_reversal_read();
DROP TABLE seller_source_reversal_dispatches;
DROP FUNCTION protect_seller_source_reversal_dispatch();
DROP FUNCTION assert_seller_source_reversal_execution(uuid,uuid);
DELETE FROM maintenance_health WHERE kind='seller_reversal_reconciliation';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation','product_settlement_reconciliation',
 'seller_funding_reconciliation','seller_bank_reconciliation'));
