-- Seller payout requests are financial commands.  Their idempotency key,
-- status history, reservations and allocations must remain addressable after
-- completion, cancellation or reconciliation.  There is no supported delete
-- operation; reject SQL-level deletes as well so an operator cannot erase the
-- evidence and accidentally make the same request key reusable.
CREATE FUNCTION reject_seller_payout_request_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'seller payout request evidence is immutable' USING ERRCODE='55000';
END;
$$;

CREATE TRIGGER seller_payout_request_delete_guard
BEFORE DELETE ON seller_payout_requests
FOR EACH ROW EXECUTE FUNCTION reject_seller_payout_request_delete();
