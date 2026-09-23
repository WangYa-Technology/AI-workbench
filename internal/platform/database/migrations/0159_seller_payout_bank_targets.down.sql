LOCK TABLE seller_payout_bank_targets IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_payout_bank_targets) THEN
    RAISE EXCEPTION 'seller payout bank evidence must be retained' USING ERRCODE='55000';
  END IF;
END;
$$;
DROP TRIGGER seller_payout_transfer_z_bank_guard ON seller_payout_transfers;
DROP FUNCTION protect_seller_payout_source_bank_binding();
DROP TABLE seller_payout_bank_targets;
DROP FUNCTION protect_seller_payout_bank_target();
