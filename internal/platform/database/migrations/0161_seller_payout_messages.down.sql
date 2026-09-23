LOCK TABLE seller_payout_reviews IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_payout_reviews WHERE seller_message<>'') THEN
  RAISE EXCEPTION 'seller payout messages must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
DROP TRIGGER seller_payout_message_required ON seller_payout_reviews;
DROP FUNCTION require_seller_payout_message();
ALTER TABLE seller_payout_reviews DROP COLUMN seller_message;
