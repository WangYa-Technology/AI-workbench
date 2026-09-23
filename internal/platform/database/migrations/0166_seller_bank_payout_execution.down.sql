LOCK TABLE seller_bank_payout_dispatches, seller_bank_payout_reads, seller_bank_payout_results, seller_ledger_entries IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_bank_payout_dispatches) OR EXISTS(SELECT 1 FROM seller_bank_payout_reads)
 OR EXISTS(SELECT 1 FROM seller_bank_payout_results)
 OR EXISTS(SELECT 1 FROM seller_ledger_entries WHERE entry_type IN ('payout_debit','payout_return')) THEN
  RAISE EXCEPTION 'bank execution evidence must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
DROP VIEW seller_bank_payout_check_candidates;
DROP TABLE seller_bank_payout_checks;
DROP FUNCTION protect_seller_bank_check();
DELETE FROM maintenance_health WHERE kind='seller_bank_reconciliation';
ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation','product_settlement_reconciliation',
 'seller_funding_reconciliation'));
CREATE OR REPLACE FUNCTION protect_seller_payout_transfer_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id) AND (
    (NEW.status IN ('requested','under_review','cancelled') AND NEW.status<>OLD.status)
    OR (NEW.status='failed' AND EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id AND status<>'failed'))
    OR (NEW.status='succeeded' AND EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id AND status<>'succeeded'))
    OR (OLD.status IN ('failed','succeeded') AND NEW.status<>OLD.status)
    OR (OLD.status='reconciliation_required' AND NEW.status='processing')
  ) THEN
    RAISE EXCEPTION 'seller payout request conflicts with transfer evidence' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION reject_unconfirmed_seller_bank_payout() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status='succeeded' THEN
    RAISE EXCEPTION 'bank payout confirmation is not implemented' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE OR REPLACE VIEW seller_funds_ledger_scopes AS
SELECT e.id AS ledger_id,e.seller_id,e.entry_type,e.amount_cents,e.currency,e.settlement_id,e.payout_request_id,e.available_at,
 CASE WHEN e.entry_type='settlement_credit' AND e.seller_id=s.seller_id AND e.currency=s.currency AND e.amount_cents=ps.net_amount_cents
   THEN s.account_id
 WHEN e.entry_type IN ('payout_reservation','payout_release') AND e.seller_id=r.seller_id AND e.currency=r.currency AND e.amount_cents=r.amount_cents
   THEN r.account_id
 ELSE NULL END AS account_id
FROM seller_ledger_entries e LEFT JOIN product_settlements ps ON ps.id=e.settlement_id
LEFT JOIN seller_funds_settlement_scopes s ON s.settlement_id=e.settlement_id
LEFT JOIN seller_funds_request_scopes r ON r.payout_request_id=e.payout_request_id;

DROP TRIGGER seller_bank_ledger_guard ON seller_ledger_entries;
DROP FUNCTION protect_seller_bank_ledger();
DROP INDEX seller_ledger_bank_once;
ALTER TABLE seller_ledger_entries DROP CONSTRAINT seller_ledger_entries_entry_type_check;
ALTER TABLE seller_ledger_entries DROP CONSTRAINT seller_ledger_entries_check1;
ALTER TABLE seller_ledger_entries ADD CONSTRAINT seller_ledger_entries_entry_type_check CHECK(entry_type IN ('settlement_credit','recovery_debit','payout_reservation','payout_release','adjustment'));
ALTER TABLE seller_ledger_entries ADD CONSTRAINT seller_ledger_entries_check1 CHECK((entry_type IN ('payout_reservation','payout_release'))=(payout_request_id IS NOT NULL));
DROP TABLE seller_bank_payout_results;
DROP FUNCTION check_seller_bank_result_consumed();
DROP FUNCTION protect_seller_bank_result();
DROP TABLE seller_bank_payout_reads;
DROP FUNCTION protect_seller_bank_read();
DROP TABLE seller_bank_payout_dispatches;
DROP FUNCTION protect_seller_bank_dispatch();
