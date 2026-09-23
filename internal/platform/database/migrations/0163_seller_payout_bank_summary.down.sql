LOCK TABLE seller_payout_bank_targets IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_payout_bank_targets WHERE bank_name IS NOT NULL OR last4 IS NOT NULL) THEN
    RAISE EXCEPTION 'frozen bank display evidence must be retained' USING ERRCODE='55000';
  END IF;
END;
$$;
DROP TRIGGER seller_payout_bank_summary_guard ON seller_payout_bank_targets;
DROP FUNCTION require_seller_payout_bank_summary();
ALTER TABLE seller_payout_bank_targets DROP CONSTRAINT seller_payout_bank_summary_valid,
  DROP COLUMN bank_name, DROP COLUMN last4;
