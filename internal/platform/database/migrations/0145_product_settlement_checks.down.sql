DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_settlement_checks) OR EXISTS(SELECT 1 FROM jobs WHERE kind='payment.check_product_settlement')
 THEN RAISE EXCEPTION 'cannot discard product settlement check evidence'; END IF;
END $$;
DROP VIEW product_settlement_check_candidates;
DROP TABLE product_settlement_checks;
DROP FUNCTION protect_product_settlement_check();
DELETE FROM maintenance_health WHERE kind='product_settlement_reconciliation';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation'));
