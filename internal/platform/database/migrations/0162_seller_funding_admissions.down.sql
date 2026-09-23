LOCK TABLE seller_payout_funding_admissions IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_payout_funding_admissions) THEN
  RAISE EXCEPTION 'seller funding admissions must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
DROP TRIGGER seller_funding_approval_guard ON seller_payout_funding_dispatches;
DROP FUNCTION require_seller_funding_admission();
DROP TABLE seller_payout_funding_admissions;
DROP FUNCTION require_seller_funding_admission_job();
DROP FUNCTION protect_seller_funding_admission();
DROP FUNCTION assert_seller_funding_approval(uuid,uuid,uuid);
