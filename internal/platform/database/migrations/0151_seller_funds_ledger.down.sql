DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_ledger_entries)
     OR EXISTS(SELECT 1 FROM seller_payout_requests)
     OR EXISTS(SELECT 1 FROM seller_payout_request_events)
     OR EXISTS(SELECT 1 FROM seller_recovery_obligations)
     OR EXISTS(SELECT 1 FROM seller_funds_reconciliations) THEN
    RAISE EXCEPTION 'seller funds evidence prevents ledger downgrade' USING ERRCODE='55000';
  END IF;
END;
$$;
DROP TABLE IF EXISTS seller_funds_reconciliations;
DROP TABLE IF EXISTS seller_recovery_obligations;
DROP TABLE IF EXISTS seller_ledger_entries;
DROP TABLE IF EXISTS seller_payout_request_events;
DROP TABLE IF EXISTS seller_payout_requests;
DROP FUNCTION IF EXISTS seller_payout_request_event_guard();
DROP FUNCTION IF EXISTS reject_seller_payout_event_mutation();
DROP FUNCTION IF EXISTS protect_seller_payout_request_identity();
DROP FUNCTION IF EXISTS reject_seller_ledger_mutation();
