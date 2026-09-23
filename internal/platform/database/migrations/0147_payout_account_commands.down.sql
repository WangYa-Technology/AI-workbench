DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM payout_account_commands) THEN
   RAISE EXCEPTION 'cannot discard payout account creation evidence';
 END IF;
END $$;
DROP TABLE payout_account_commands;
DROP FUNCTION protect_payout_account_command();
