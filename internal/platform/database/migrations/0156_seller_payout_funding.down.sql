DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM seller_payout_funding_dispatches) THEN
    RAISE EXCEPTION 'seller funding evidence prevents downgrade' USING ERRCODE='55000';
  END IF;
END;
$$;
DROP TRIGGER seller_payout_bank_confirmation_guard ON seller_payout_requests;
DROP FUNCTION reject_unconfirmed_seller_bank_payout();
DROP TABLE seller_payout_funding_reads;
DROP FUNCTION protect_seller_payout_funding_read();
DROP TABLE seller_payout_funding_dispatches;
DROP FUNCTION protect_seller_payout_funding_dispatch();
