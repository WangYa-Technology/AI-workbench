LOCK TABLE wallet_topup_settings IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM wallet_topup_settings WHERE version <> 1 OR minimum_amount_cents <> 50 OR preset_amounts_cents <> ARRAY[1000,2000,5000,10000,20000])
    OR EXISTS(SELECT 1 FROM audit_events WHERE action='admin.wallet_topup_settings_updated') THEN
  RAISE EXCEPTION 'cannot discard configured wallet top-up rules and revision evidence';
 END IF;
END $$;
DROP TRIGGER wallet_topup_minimum_guard ON payment_intents;
DROP FUNCTION check_wallet_topup_minimum();
DROP TABLE wallet_topup_settings;
DROP FUNCTION valid_wallet_topup_amounts(integer,integer[]);
