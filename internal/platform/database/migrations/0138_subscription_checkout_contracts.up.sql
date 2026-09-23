-- Historical plan values cannot prove the terms accepted by an earlier buyer.
-- Checkout already accepts keys through 128 bytes; fulfillment must accept
-- those same commands instead of failing after the provider has collected funds.
ALTER TABLE user_subscriptions DROP CONSTRAINT user_subscriptions_idempotency_key_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_idempotency_key_check
 CHECK (char_length(idempotency_key) BETWEEN 8 AND 128);

CREATE TABLE subscription_checkout_contracts (
 payment_id uuid PRIMARY KEY REFERENCES payment_intents(id),
 buyer_id uuid NOT NULL REFERENCES users(id),
 plan_id uuid NOT NULL REFERENCES subscription_plans(id),
 plan_name text NOT NULL CHECK (char_length(plan_name) BETWEEN 2 AND 80),
 tier_code text NOT NULL,
 description text NOT NULL,
 price_cents integer NOT NULL CHECK (price_cents >= 50),
 currency text NOT NULL CHECK (currency='USD'),
 included_points bigint NOT NULL CHECK (included_points > 0),
 billing_period_days integer NOT NULL CHECK (billing_period_days BETWEEN 1 AND 366),
 model_ids uuid[] NOT NULL CHECK (array_position(model_ids,NULL) IS NULL),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER subscription_checkout_contracts_immutable
 BEFORE UPDATE OR DELETE ON subscription_checkout_contracts
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE FUNCTION require_subscription_checkout_contract() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.purpose='subscription' AND NOT EXISTS (
  SELECT 1 FROM subscription_checkout_contracts c WHERE c.payment_id=NEW.id
   AND c.buyer_id=NEW.payer_id AND c.plan_id=NEW.resource_id
   AND c.price_cents=NEW.amount_cents AND c.currency=NEW.currency
 ) THEN
  RAISE EXCEPTION 'subscription checkout requires original contract' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER payment_intents_subscription_contract
 AFTER INSERT ON payment_intents DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION require_subscription_checkout_contract();

CREATE FUNCTION preserve_subscription_payment_contract() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.purpose,NEW.payer_id,NEW.resource_id,NEW.amount_cents,NEW.currency,NEW.provider,NEW.live_mode,NEW.idempotency_key)
    IS DISTINCT FROM ROW(OLD.purpose,OLD.payer_id,OLD.resource_id,OLD.amount_cents,OLD.currency,OLD.provider,OLD.live_mode,OLD.idempotency_key)
    AND EXISTS(SELECT 1 FROM subscription_checkout_contracts WHERE payment_id=OLD.id) THEN
  RAISE EXCEPTION 'accepted subscription payment terms are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER payment_intents_subscription_contract_binding
 BEFORE UPDATE ON payment_intents FOR EACH ROW EXECUTE FUNCTION preserve_subscription_payment_contract();
