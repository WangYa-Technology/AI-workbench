DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_payout_request_allocations)
     OR EXISTS(SELECT 1 FROM seller_payout_transfers) THEN
    RAISE EXCEPTION 'seller payout evidence prevents allocation downgrade' USING ERRCODE='55000';
  END IF;
END;
$$;
DROP TRIGGER seller_payout_cancelled_allocations ON seller_payout_requests;
DROP FUNCTION release_cancelled_seller_payout_allocations();
DROP TRIGGER seller_payout_cancellation_guard ON seller_payout_requests;
DROP FUNCTION protect_seller_payout_cancellation();
DROP TRIGGER seller_payout_allocation_guard ON seller_payout_request_allocations;
DROP FUNCTION protect_seller_payout_allocation();
DROP INDEX seller_payout_allocation_active_uq;
ALTER TABLE seller_payout_request_allocations DROP COLUMN released_at;
ALTER TABLE seller_payout_request_allocations ADD UNIQUE (settlement_id);
