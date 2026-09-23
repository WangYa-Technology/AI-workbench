DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM seller_payout_requests) THEN
    RAISE EXCEPTION 'seller payout request evidence prevents downgrade' USING ERRCODE='55000';
  END IF;
END;
$$;

DROP TRIGGER seller_payout_request_delete_guard ON seller_payout_requests;
DROP FUNCTION reject_seller_payout_request_delete();
