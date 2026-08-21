CREATE TABLE subscription_plans (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tier_code text NOT NULL CHECK (tier_code ~ '^[a-z0-9][a-z0-9_-]{1,31}$'),
  name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 80),
  description text NOT NULL CHECK (char_length(description) BETWEEN 1 AND 500),
  price_cents integer NOT NULL CHECK (price_cents >= 0),
  currency text NOT NULL DEFAULT 'USD' CHECK (char_length(currency)=3),
  included_points bigint NOT NULL CHECK (included_points > 0),
  billing_period_days integer NOT NULL DEFAULT 30 CHECK (billing_period_days BETWEEN 1 AND 366),
  sort_order integer NOT NULL DEFAULT 0,
  active boolean NOT NULL DEFAULT true,
  created_by uuid REFERENCES users(id),
  updated_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(tier_code)
);

ALTER TABLE billing_entries DROP CONSTRAINT billing_entries_entry_type_check;
ALTER TABLE billing_entries ADD CONSTRAINT billing_entries_entry_type_check CHECK (entry_type IN (
  'generation_charge','product_purchase','product_sale','product_refund',
  'task_payment','task_earning','admin_adjustment','initial_credit','subscription_purchase'
));

CREATE TABLE subscription_plan_models (
  plan_id uuid NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
  provider_model_id uuid NOT NULL REFERENCES provider_config_models(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(plan_id,provider_model_id)
);
CREATE INDEX subscription_plan_models_model_idx ON subscription_plan_models(provider_model_id,plan_id);

CREATE TABLE point_accounts (
  user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  balance_points bigint NOT NULL DEFAULT 0 CHECK (balance_points >= 0),
  reserved_points bigint NOT NULL DEFAULT 0 CHECK (reserved_points >= 0 AND reserved_points <= balance_points),
  lifetime_earned_points bigint NOT NULL DEFAULT 0 CHECK (lifetime_earned_points >= 0),
  lifetime_spent_points bigint NOT NULL DEFAULT 0 CHECK (lifetime_spent_points >= 0),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_subscriptions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_id uuid NOT NULL REFERENCES subscription_plans(id) ON DELETE RESTRICT,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','expired','cancelled')),
  price_cents integer NOT NULL CHECK (price_cents >= 0),
  currency text NOT NULL CHECK (char_length(currency)=3),
  granted_points bigint NOT NULL CHECK (granted_points > 0),
  started_at timestamptz NOT NULL DEFAULT now(),
  current_period_end timestamptz NOT NULL,
  cancelled_at timestamptz,
  purchase_operation_id uuid NOT NULL UNIQUE,
  idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 120),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(user_id,idempotency_key)
);
CREATE UNIQUE INDEX user_subscriptions_one_active_idx ON user_subscriptions(user_id) WHERE status='active';
CREATE INDEX user_subscriptions_user_history_idx ON user_subscriptions(user_id,created_at DESC,id DESC);

CREATE TABLE point_entries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  operation_id uuid NOT NULL,
  entry_type text NOT NULL CHECK (entry_type IN ('subscription_credit','generation_charge','admin_adjustment','migration_grant')),
  direction text NOT NULL CHECK (direction IN ('debit','credit')),
  amount_points bigint NOT NULL CHECK (amount_points > 0),
  balance_after_points bigint NOT NULL CHECK (balance_after_points >= 0),
  description text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(user_id,operation_id,entry_type,direction)
);
CREATE INDEX point_entries_user_idx ON point_entries(user_id,created_at DESC,id DESC);

CREATE TABLE model_point_pricing_rules (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_model_id uuid UNIQUE REFERENCES provider_config_models(id) ON DELETE CASCADE,
  provider_profile_id text UNIQUE REFERENCES provider_profiles(id) ON DELETE CASCADE,
  mode text NOT NULL CHECK (mode IN ('chat','image','video','music')),
  input_points_per_1k_tokens integer NOT NULL DEFAULT 0 CHECK (input_points_per_1k_tokens >= 0),
  output_points_per_1k_tokens integer NOT NULL DEFAULT 0 CHECK (output_points_per_1k_tokens >= 0),
  points_per_second integer NOT NULL DEFAULT 0 CHECK (points_per_second >= 0),
  minimum_points integer NOT NULL DEFAULT 1 CHECK (minimum_points > 0),
  image_resolution_prices jsonb NOT NULL DEFAULT '[]'::jsonb,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  created_by uuid REFERENCES users(id),
  updated_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((provider_model_id IS NOT NULL) <> (provider_profile_id IS NOT NULL)),
  CHECK (
    (mode='chat' AND input_points_per_1k_tokens > 0 AND output_points_per_1k_tokens > 0 AND points_per_second=0 AND image_resolution_prices='[]'::jsonb) OR
    (mode='image' AND input_points_per_1k_tokens=0 AND output_points_per_1k_tokens=0 AND points_per_second=0 AND jsonb_array_length(image_resolution_prices) > 0) OR
    (mode IN ('video','music') AND input_points_per_1k_tokens=0 AND output_points_per_1k_tokens=0 AND points_per_second > 0 AND image_resolution_prices='[]'::jsonb)
  )
);

CREATE TABLE point_reservations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  generation_id uuid NOT NULL UNIQUE REFERENCES generations(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
  subscription_id uuid NOT NULL REFERENCES user_subscriptions(id) ON DELETE RESTRICT,
  held_points bigint NOT NULL CHECK (held_points > 0),
  charged_points bigint NOT NULL DEFAULT 0 CHECK (charged_points >= 0),
  status text NOT NULL DEFAULT 'held' CHECK (status IN ('held','captured','released')),
  pricing_snapshot jsonb NOT NULL,
  usage_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
  release_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX point_reservations_user_idx ON point_reservations(user_id,created_at DESC);

ALTER TABLE generations
  ADD COLUMN provider_model_id uuid REFERENCES provider_config_models(id),
  ADD COLUMN estimated_points bigint NOT NULL DEFAULT 0 CHECK (estimated_points >= 0),
  ADD COLUMN charged_points bigint NOT NULL DEFAULT 0 CHECK (charged_points >= 0),
  ADD COLUMN point_pricing_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN point_usage_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb;

INSERT INTO subscription_plans(tier_code,name,description,price_cents,included_points,sort_order)
VALUES
  ('starter','Starter','A practical starting allowance for occasional AI creation.',0,10000,10),
  ('creator','Creator','More monthly points and access to the models selected by the workspace administrator.',2000,100000,20),
  ('studio','Studio','A larger point allowance for frequent multimodal production.',6000,400000,30);

INSERT INTO subscription_plan_models(plan_id,provider_model_id)
SELECT p.id,m.id FROM subscription_plans p CROSS JOIN provider_config_models m WHERE p.tier_code IN ('starter','creator','studio');

INSERT INTO model_point_pricing_rules(provider_model_id,mode,input_points_per_1k_tokens,output_points_per_1k_tokens,points_per_second,minimum_points,image_resolution_prices)
SELECT id,mode,
  CASE WHEN mode='chat' THEN GREATEST(1,estimated_cost_cents*2) ELSE 0 END,
  CASE WHEN mode='chat' THEN GREATEST(2,estimated_cost_cents*8) ELSE 0 END,
  CASE WHEN mode IN ('video','music') THEN GREATEST(1,estimated_cost_cents) ELSE 0 END,
  GREATEST(1,estimated_cost_cents*10),
  CASE WHEN mode='image' THEN jsonb_build_array(
    jsonb_build_object('resolution','1024x1024','points',GREATEST(10,estimated_cost_cents*10)),
    jsonb_build_object('resolution','1024x1536','points',GREATEST(15,estimated_cost_cents*15)),
    jsonb_build_object('resolution','1536x1024','points',GREATEST(15,estimated_cost_cents*15))
  ) ELSE '[]'::jsonb END
FROM provider_config_models;

INSERT INTO model_point_pricing_rules(provider_profile_id,mode,input_points_per_1k_tokens,output_points_per_1k_tokens,points_per_second,minimum_points,image_resolution_prices)
SELECT id,mode,
  CASE WHEN mode='chat' THEN GREATEST(1,estimated_cost_cents*2) ELSE 0 END,
  CASE WHEN mode='chat' THEN GREATEST(2,estimated_cost_cents*8) ELSE 0 END,
  CASE WHEN mode IN ('video','music') THEN GREATEST(1,estimated_cost_cents) ELSE 0 END,
  GREATEST(1,estimated_cost_cents*10),
  CASE WHEN mode='image' THEN jsonb_build_array(
    jsonb_build_object('resolution','1024x1024','points',GREATEST(10,estimated_cost_cents*10)),
    jsonb_build_object('resolution','1024x1536','points',GREATEST(15,estimated_cost_cents*15)),
    jsonb_build_object('resolution','1536x1024','points',GREATEST(15,estimated_cost_cents*15))
  ) ELSE '[]'::jsonb END
FROM provider_profiles;

INSERT INTO point_accounts(user_id,balance_points,lifetime_earned_points)
SELECT id,10000,10000 FROM users;

INSERT INTO user_subscriptions(user_id,plan_id,price_cents,currency,granted_points,current_period_end,purchase_operation_id,idempotency_key)
SELECT u.id,p.id,0,'USD',p.included_points,now()+make_interval(days => p.billing_period_days),gen_random_uuid(),'migration-starter'
FROM users u CROSS JOIN subscription_plans p WHERE p.tier_code='starter';

INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata)
SELECT s.user_id,s.purchase_operation_id,'migration_grant','credit',s.granted_points,s.granted_points,'Starter subscription migration grant',jsonb_build_object('subscriptionId',s.id,'planId',s.plan_id)
FROM user_subscriptions s WHERE s.idempotency_key='migration-starter';

CREATE FUNCTION create_default_point_account_and_subscription() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  starter subscription_plans%ROWTYPE;
  subscription_id uuid := gen_random_uuid();
  operation_id uuid := gen_random_uuid();
BEGIN
  SELECT * INTO starter FROM subscription_plans WHERE tier_code='starter';
  INSERT INTO point_accounts(user_id,balance_points,lifetime_earned_points)
  VALUES(NEW.id,starter.included_points,starter.included_points);
  INSERT INTO user_subscriptions(id,user_id,plan_id,price_cents,currency,granted_points,current_period_end,purchase_operation_id,idempotency_key)
  VALUES(subscription_id,NEW.id,starter.id,starter.price_cents,starter.currency,starter.included_points,now()+make_interval(days => starter.billing_period_days),operation_id,'signup-starter');
  INSERT INTO point_entries(user_id,operation_id,entry_type,direction,amount_points,balance_after_points,description,metadata)
  VALUES(NEW.id,operation_id,'subscription_credit','credit',starter.included_points,starter.included_points,'Starter subscription points',jsonb_build_object('subscriptionId',subscription_id,'planId',starter.id));
  RETURN NEW;
END;
$$;

CREATE TRIGGER users_create_default_point_account_and_subscription
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION create_default_point_account_and_subscription();
