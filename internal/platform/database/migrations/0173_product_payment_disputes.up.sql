ALTER TABLE payment_provider_events
  ADD COLUMN dispute_status text CHECK (dispute_status IS NULL OR dispute_status ~ '^[a-z0-9_]{2,80}$'),
  ADD COLUMN dispute_reason text CHECK (dispute_reason IS NULL OR dispute_reason ~ '^[a-z0-9_]{2,80}$'),
  ADD COLUMN dispute_network_reason_code text CHECK (dispute_network_reason_code IS NULL OR dispute_network_reason_code ~ '^[a-z0-9_]{2,80}$'),
  ADD COLUMN dispute_due_by timestamptz;

CREATE TABLE product_payment_disputes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL CHECK (provider='stripe'),
  live_mode boolean NOT NULL,
  provider_dispute_id text NOT NULL CHECK (provider_dispute_id ~ '^dp_[A-Za-z0-9_]+$'),
  payment_id uuid REFERENCES payment_intents(id),
  order_id uuid REFERENCES orders(id),
  seller_id uuid REFERENCES users(id),
  settlement_id uuid REFERENCES product_settlements(id),
  provider_payment_id text NOT NULL CHECK (provider_payment_id ~ '^pi_[A-Za-z0-9_]+$'),
  provider_charge_id text NOT NULL CHECK (provider_charge_id ~ '^ch_[A-Za-z0-9_]+$'),
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 99999999),
  currency text NOT NULL CHECK (currency='USD'),
  provider_status text NOT NULL CHECK (provider_status ~ '^[a-z0-9_]{2,80}$'),
  action_status text NOT NULL CHECK (action_status IN ('needs_response','warning_needs_response','under_review','warning_under_review','won','lost','charge_refunded','prevented','requires_review')),
  due_by timestamptz NOT NULL,
  latest_event_at timestamptz NOT NULL,
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(provider,live_mode,provider_dispute_id)
);
CREATE INDEX product_payment_disputes_payment_idx ON product_payment_disputes(payment_id,latest_event_at DESC);
CREATE INDEX product_payment_disputes_open_idx ON product_payment_disputes(settlement_id,action_status)
 WHERE action_status <> 'won';

CREATE TABLE product_payment_dispute_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  dispute_id uuid NOT NULL REFERENCES product_payment_disputes(id),
  provider_event_id uuid NOT NULL UNIQUE REFERENCES payment_provider_events(id),
  event_type text NOT NULL CHECK (event_type ~ '^[a-z0-9_.]{3,120}$'),
  provider_status text NOT NULL CHECK (provider_status ~ '^[a-z0-9_]{2,80}$'),
  reason text NOT NULL CHECK (reason ~ '^[a-z0-9_]{2,80}$'),
  network_reason_code text NOT NULL CHECK (network_reason_code ~ '^[a-z0-9_]{2,80}$'),
  due_by timestamptz NOT NULL,
  occurred_at timestamptz NOT NULL,
  applied boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(dispute_id,provider_event_id)
);
CREATE INDEX product_payment_dispute_events_order_idx ON product_payment_dispute_events(dispute_id,occurred_at DESC,id DESC);

CREATE FUNCTION reject_product_payment_dispute_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'product payment dispute evidence is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER product_payment_dispute_events_immutable
BEFORE UPDATE OR DELETE ON product_payment_dispute_events
FOR EACH ROW EXECUTE FUNCTION reject_product_payment_dispute_mutation();

ALTER TABLE product_settlements DROP CONSTRAINT IF EXISTS product_settlements_status_check;
ALTER TABLE product_settlements ADD CONSTRAINT product_settlements_status_check CHECK (status IN (
  'pending_hold','available','dispute_hold','transfer_pending','transferred','refund_hold',
  'recovery_required','provider_unsupported','cancelled'));

CREATE OR REPLACE FUNCTION protect_product_settlement_dispute_hold() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IN ('transfer_pending','transferred') AND EXISTS (
    SELECT 1 FROM product_payment_disputes d
    WHERE d.settlement_id=NEW.id AND d.action_status <> 'won'
  ) THEN
    RAISE EXCEPTION 'unresolved product payment dispute blocks settlement transfer' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER product_settlement_dispute_hold_guard
BEFORE INSERT OR UPDATE ON product_settlements
FOR EACH ROW EXECUTE FUNCTION protect_product_settlement_dispute_hold();
