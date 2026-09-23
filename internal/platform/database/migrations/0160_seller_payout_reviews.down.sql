LOCK TABLE seller_payout_reviews IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_payout_reviews) THEN
  RAISE EXCEPTION 'seller payout review evidence must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
DROP TABLE seller_payout_reviews;
DROP FUNCTION protect_seller_payout_review();
DROP INDEX seller_payout_requests_review_directory_idx;
