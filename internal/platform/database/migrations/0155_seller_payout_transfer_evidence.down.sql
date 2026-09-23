DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM seller_payout_transfers) THEN
    RAISE EXCEPTION 'seller payout transfer evidence prevents downgrade' USING ERRCODE='55000';
  END IF;
END;
$$;
DROP TRIGGER seller_payout_transfer_request_guard ON seller_payout_requests;
DROP FUNCTION protect_seller_payout_transfer_request();
DROP TRIGGER seller_payout_transfer_evidence_guard ON seller_payout_transfers;
DROP FUNCTION protect_seller_payout_transfer_evidence();
ALTER TABLE seller_payout_transfers
  DROP CONSTRAINT seller_payout_transfer_result_shape,
  DROP CONSTRAINT seller_payout_transfer_identity_shape,
  DROP COLUMN reserved_at,
  DROP COLUMN dispatch_key,
  DROP COLUMN provider_identity;
