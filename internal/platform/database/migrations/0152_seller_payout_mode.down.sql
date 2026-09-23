DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_payout_transfers)
     OR EXISTS(SELECT 1 FROM seller_payout_request_allocations)
     OR EXISTS(SELECT 1 FROM seller_payout_requests) THEN
    RAISE EXCEPTION 'seller payout evidence prevents payout mode downgrade' USING ERRCODE='55000';
  END IF;
END;
$$;
DROP TABLE IF EXISTS seller_payout_transfers;
DROP FUNCTION IF EXISTS reject_seller_payout_transfer_mutation();
DROP TABLE IF EXISTS seller_payout_request_allocations;
ALTER TABLE product_settlement_settings DROP COLUMN IF EXISTS payout_mode;
