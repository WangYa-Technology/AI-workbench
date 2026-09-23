LOCK TABLE seller_bank_payout_commands IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_bank_payout_commands) THEN
  RAISE EXCEPTION 'seller bank payout commands must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
DROP FUNCTION assert_seller_bank_payout_command(seller_bank_payout_commands);
DROP TABLE seller_bank_payout_commands;
DROP FUNCTION protect_seller_bank_payout_command();
