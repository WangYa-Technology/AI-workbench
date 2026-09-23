LOCK TABLE subscription_checkout_contracts IN ACCESS EXCLUSIVE MODE;
LOCK TABLE user_subscriptions IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM subscription_checkout_contracts) THEN
  RAISE EXCEPTION 'accepted subscription contracts prevent downgrade';
 END IF;
 IF EXISTS (SELECT 1 FROM user_subscriptions WHERE char_length(idempotency_key)>120) THEN
  RAISE EXCEPTION 'accepted long subscription commands prevent downgrade';
 END IF;
END $$;
DROP TRIGGER payment_intents_subscription_contract_binding ON payment_intents;
DROP FUNCTION preserve_subscription_payment_contract();
DROP TRIGGER payment_intents_subscription_contract ON payment_intents;
DROP FUNCTION require_subscription_checkout_contract();
DROP TABLE subscription_checkout_contracts;
ALTER TABLE user_subscriptions DROP CONSTRAINT user_subscriptions_idempotency_key_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_idempotency_key_check
 CHECK (char_length(idempotency_key) BETWEEN 8 AND 120);
