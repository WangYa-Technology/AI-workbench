ALTER TABLE generations
  ADD COLUMN retry_of_generation_id uuid REFERENCES generations(id),
  ADD COLUMN cancelled_at timestamptz,
  ADD COLUMN cancel_reason text;

CREATE INDEX generations_retry_idx
  ON generations(retry_of_generation_id) WHERE retry_of_generation_id IS NOT NULL;

CREATE TABLE billing_accounts (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  currency text NOT NULL DEFAULT 'USD' CHECK (char_length(currency)=3),
  balance_cents bigint NOT NULL DEFAULT 0 CHECK (balance_cents >= 0),
  reserved_cents bigint NOT NULL DEFAULT 0 CHECK (reserved_cents >= 0 AND reserved_cents <= balance_cents),
  payment_mode text NOT NULL DEFAULT 'local_test' CHECK (payment_mode='local_test'),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id,currency)
);

INSERT INTO billing_accounts(user_id,currency,balance_cents)
SELECT id,'USD',250000 FROM users
ON CONFLICT (user_id,currency) DO NOTHING;

CREATE FUNCTION create_local_test_billing_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO billing_accounts(user_id,currency,balance_cents)
  VALUES(NEW.id,'USD',250000)
  ON CONFLICT (user_id,currency) DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE TRIGGER users_create_local_test_billing_account
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION create_local_test_billing_account();

CREATE TABLE billing_reservations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  operation_type text NOT NULL CHECK (operation_type IN ('generation')),
  operation_id uuid NOT NULL,
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL CHECK (char_length(currency)=3),
  status text NOT NULL DEFAULT 'held' CHECK (status IN ('held','captured','released')),
  release_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(operation_type,operation_id)
);

CREATE INDEX billing_reservations_user_idx ON billing_reservations(user_id,created_at DESC);

CREATE TABLE billing_entries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  operation_id uuid NOT NULL,
  entry_type text NOT NULL CHECK (entry_type IN (
    'generation_charge','product_purchase','product_sale','product_refund',
    'task_payment','task_earning','admin_adjustment','initial_credit'
  )),
  direction text NOT NULL CHECK (direction IN ('debit','credit')),
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL CHECK (char_length(currency)=3),
  balance_after_cents bigint NOT NULL CHECK (balance_after_cents >= 0),
  description text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(user_id,operation_id,entry_type,direction)
);

CREATE INDEX billing_entries_user_idx ON billing_entries(user_id,created_at DESC,id DESC);

INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description)
SELECT user_id,gen_random_uuid(),
       'initial_credit','credit',balance_cents,currency,balance_cents,'Local Test opening credit'
FROM billing_accounts WHERE balance_cents > 0;

CREATE TABLE generation_commands (
  actor_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  operation text NOT NULL CHECK (operation IN ('submit','cancel','retry')),
  idempotency_key text NOT NULL,
  generation_id uuid NOT NULL REFERENCES generations(id) ON DELETE CASCADE,
  result_generation_id uuid REFERENCES generations(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(actor_id,operation,idempotency_key)
);

CREATE TABLE provider_profiles (
  id text PRIMARY KEY,
  mode text NOT NULL CHECK (mode IN ('chat','image','video','music')),
  provider text NOT NULL,
  model_name text NOT NULL,
  display_name text NOT NULL,
  description text NOT NULL,
  estimated_cost_cents integer NOT NULL CHECK (estimated_cost_cents >= 0),
  currency text NOT NULL DEFAULT 'USD' CHECK (char_length(currency)=3),
  local_test boolean NOT NULL DEFAULT false,
  admin_enabled boolean NOT NULL DEFAULT false,
  updated_by uuid REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(mode,provider,model_name)
);

INSERT INTO provider_profiles(id,mode,provider,model_name,display_name,description,estimated_cost_cents,local_test,admin_enabled)
VALUES
  ('local-image-v1','image','local_test','hcai-local-image-v1','Local Image Test','Deterministic local image output for workflow verification.',5,true,true),
  ('openai-image','image','openai','gpt-image','OpenAI Image','Production adapter requires approved credentials and paid-call authorization.',0,false,false),
  ('openai-chat','chat','openai','gpt-chat','OpenAI Chat','Production adapter requires approved credentials and paid-call authorization.',0,false,false),
  ('video-provider','video','external','video-model','Video generation','No approved production provider is configured.',0,false,false),
  ('music-provider','music','external','music-model','Music generation','No approved production provider is configured.',0,false,false);

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('billing:self','billing','Read the current account credit balance and statement','low',true),
  ('admin:overview','admin','Read operational health and aggregate counts','low',false),
  ('admin:users','admin','Read users and change account access or roles','high',true),
  ('admin:content','admin','Review and moderate published works and posts','high',true),
  ('admin:generations','admin','Inspect and cancel generation jobs','high',true),
  ('admin:providers','admin','Inspect and change provider availability','high',true),
  ('admin:finance','admin','Read Local Test accounts and adjust test credits','high',true),
  ('admin:audit','admin','Read immutable audit evidence','medium',false)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
SELECT role,'billing:self' FROM (VALUES ('member'),('creator'),('publisher'),('moderator'),('admin')) AS roles(role)
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
SELECT 'admin',id FROM permissions WHERE id LIKE 'admin:%'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('moderator','admin:overview'),('moderator','admin:content'),('moderator','admin:audit')
ON CONFLICT DO NOTHING;
