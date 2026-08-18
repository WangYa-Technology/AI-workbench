ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN (
  'test_pending','test_paid','payment_pending','payment_paid','payment_failed',
  'fulfilled','refund_requested','test_refunded','refunded','cancelled'
));

ALTER TABLE order_events DROP CONSTRAINT order_events_to_status_check;
ALTER TABLE order_events ADD CONSTRAINT order_events_to_status_check CHECK (to_status IN (
  'test_pending','test_paid','payment_pending','payment_paid','payment_failed',
  'fulfilled','refund_requested','test_refunded','refunded','cancelled'
));

CREATE TABLE payment_intents (
  id uuid PRIMARY KEY,
  provider text NOT NULL CHECK (provider IN ('stripe')),
  purpose text NOT NULL CHECK (purpose IN ('product','task')),
  payer_id uuid NOT NULL REFERENCES users(id),
  payee_id uuid REFERENCES users(id),
  resource_id uuid NOT NULL,
  order_id uuid UNIQUE REFERENCES orders(id),
  proposal_id uuid REFERENCES proposals(id),
  amount_cents integer NOT NULL CHECK (amount_cents BETWEEN 50 AND 99999999),
  currency text NOT NULL CHECK (currency='USD'),
  status text NOT NULL CHECK (status IN (
    'checkout_pending','checkout_open','paid','payment_failed','transfer_pending','transferred',
    'refund_pending','refund_failed','refunded','cancelled'
  )),
  live_mode boolean NOT NULL,
  idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 128),
  provider_checkout_id text UNIQUE CHECK (provider_checkout_id IS NULL OR (char_length(provider_checkout_id) BETWEEN 6 AND 255 AND provider_checkout_id ~ '^[A-Za-z0-9_]+$')),
  checkout_url text CHECK (checkout_url IS NULL OR (char_length(checkout_url) BETWEEN 16 AND 2048 AND checkout_url LIKE 'https://checkout.stripe.com/%')),
  checkout_expires_at timestamptz,
  provider_payment_id text UNIQUE CHECK (provider_payment_id IS NULL OR (char_length(provider_payment_id) BETWEEN 6 AND 255 AND provider_payment_id ~ '^[A-Za-z0-9_]+$')),
  provider_charge_id text UNIQUE CHECK (provider_charge_id IS NULL OR (char_length(provider_charge_id) BETWEEN 6 AND 255 AND provider_charge_id ~ '^[A-Za-z0-9_]+$')),
  provider_refund_id text UNIQUE CHECK (provider_refund_id IS NULL OR (char_length(provider_refund_id) BETWEEN 6 AND 255 AND provider_refund_id ~ '^[A-Za-z0-9_]+$')),
  refund_operation_id uuid,
  provider_transfer_id text UNIQUE CHECK (provider_transfer_id IS NULL OR (char_length(provider_transfer_id) BETWEEN 6 AND 255 AND provider_transfer_id ~ '^[A-Za-z0-9_]+$')),
  destination_id text CHECK (destination_id IS NULL OR (char_length(destination_id) BETWEEN 6 AND 255 AND destination_id ~ '^[A-Za-z0-9_]+$')),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  paid_at timestamptz,
  transferred_at timestamptz,
  refunded_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(payer_id,purpose,idempotency_key),
  CHECK ((purpose='product' AND order_id IS NOT NULL AND proposal_id IS NULL AND payee_id IS NOT NULL) OR
         (purpose='task' AND order_id IS NULL)),
  CHECK (purpose<>'task' OR status<>'refund_pending' OR (provider_payment_id IS NOT NULL AND refund_operation_id IS NOT NULL)),
  CHECK ((status='checkout_open') = (provider_checkout_id IS NOT NULL AND checkout_url IS NOT NULL AND checkout_expires_at IS NOT NULL) OR status<>'checkout_open')
);
CREATE INDEX payment_intents_payer_idx ON payment_intents(payer_id,created_at DESC,id DESC);
CREATE INDEX payment_intents_resource_idx ON payment_intents(purpose,resource_id,created_at DESC,id DESC);
CREATE INDEX payment_intents_status_idx ON payment_intents(status,updated_at,id);
CREATE UNIQUE INDEX payment_intents_active_task_idx ON payment_intents(resource_id)
  WHERE purpose='task' AND status IN ('checkout_pending','checkout_open','paid','transfer_pending','transferred','refund_pending','refund_failed');

CREATE TABLE payment_destinations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL CHECK (provider='stripe'),
  user_id uuid NOT NULL REFERENCES users(id),
  destination_id text NOT NULL UNIQUE CHECK (char_length(destination_id) BETWEEN 6 AND 255 AND destination_id ~ '^acct_[A-Za-z0-9_]+$'),
  status text NOT NULL CHECK (status IN ('verified','disabled')),
  charges_enabled boolean NOT NULL DEFAULT false,
  payouts_enabled boolean NOT NULL DEFAULT false,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  verified_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider,user_id),
  CHECK ((status='verified') = (verified_at IS NOT NULL AND charges_enabled AND payouts_enabled))
);
CREATE INDEX payment_destinations_status_idx ON payment_destinations(status,updated_at,id);

CREATE TABLE payment_intent_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id uuid NOT NULL REFERENCES payment_intents(id),
  provider_event_id uuid REFERENCES payment_provider_events(id),
  event_type text NOT NULL CHECK (char_length(event_type) BETWEEN 3 AND 80 AND event_type ~ '^[a-z0-9_.]+$'),
  from_status text,
  to_status text NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(payment_id,event_type,provider_event_id)
);
CREATE INDEX payment_intent_events_payment_idx ON payment_intent_events(payment_id,created_at,id);

CREATE TRIGGER payment_intent_events_immutable
BEFORE UPDATE OR DELETE ON payment_intent_events
FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

ALTER TABLE task_settlements DROP CONSTRAINT task_settlements_mode_check;
ALTER TABLE task_settlements ADD CONSTRAINT task_settlements_mode_check
  CHECK (mode IN ('local_test','stripe_pending','stripe_transferred'));
