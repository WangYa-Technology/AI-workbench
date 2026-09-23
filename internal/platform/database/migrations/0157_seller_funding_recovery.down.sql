DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM seller_payout_funding_checks) THEN
  RAISE EXCEPTION 'seller funding recovery evidence prevents downgrade' USING ERRCODE='55000';
 END IF;
END;
$$;
DROP VIEW seller_payout_funding_check_candidates;
DROP TABLE seller_payout_funding_checks;
DROP FUNCTION protect_seller_payout_funding_check();
DELETE FROM maintenance_health WHERE kind='seller_funding_reconciliation';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation','product_settlement_reconciliation'));
