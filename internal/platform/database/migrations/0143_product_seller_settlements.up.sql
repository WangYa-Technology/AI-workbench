-- Product seller settlement is a separate financial projection.  It must not
-- be inferred from billing_accounts (which is the local wallet) or from the
-- seller sales view (which only exposes gross order amounts).
CREATE TABLE product_settlement_settings (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  platform_fee_bps integer NOT NULL DEFAULT 0 CHECK (platform_fee_bps BETWEEN 0 AND 10000),
  hold_days integer NOT NULL DEFAULT 7 CHECK (hold_days BETWEEN 0 AND 365),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_by uuid REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO product_settlement_settings(singleton) VALUES (true)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE product_payout_batches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL CHECK (provider IN ('stripe','waffo_pancake','epay')),
  live_mode boolean NOT NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','dispatching','completed','reconciliation_required','cancelled')),
  scheduled_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  started_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK ((status='completed') = (completed_at IS NOT NULL)),
  CHECK ((status IN ('open','dispatching','reconciliation_required')) OR started_at IS NOT NULL)
);
CREATE INDEX product_payout_batches_status_idx ON product_payout_batches(status,scheduled_at,id);

CREATE TABLE product_settlements (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
  payment_id uuid NOT NULL UNIQUE REFERENCES payment_intents(id),
  seller_id uuid NOT NULL REFERENCES users(id),
  provider text NOT NULL CHECK (provider IN ('stripe','waffo_pancake','epay')),
  live_mode boolean NOT NULL,
  gross_amount_cents integer NOT NULL CHECK (gross_amount_cents > 0),
  fee_bps integer NOT NULL CHECK (fee_bps BETWEEN 0 AND 10000),
  fee_cents integer NOT NULL CHECK (fee_cents >= 0 AND fee_cents <= gross_amount_cents),
  net_amount_cents integer NOT NULL CHECK (net_amount_cents = gross_amount_cents-fee_cents AND net_amount_cents >= 0),
  currency text NOT NULL CHECK (currency='USD'),
  status text NOT NULL DEFAULT 'pending_hold' CHECK (status IN (
    'pending_hold','available','transfer_pending','transferred','refund_hold',
    'recovery_required','provider_unsupported','cancelled'
  )),
  hold_reason text NOT NULL DEFAULT '',
  available_at timestamptz NOT NULL,
  payout_batch_id uuid REFERENCES product_payout_batches(id),
  destination_id text,
  provider_transfer_id text UNIQUE,
  transfer_idempotency_key text NOT NULL UNIQUE,
  recovery_amount_cents integer NOT NULL DEFAULT 0 CHECK (recovery_amount_cents >= 0),
  transferred_at timestamptz,
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK ((status='transferred') = (transferred_at IS NOT NULL AND provider_transfer_id IS NOT NULL)),
  CHECK (recovery_amount_cents <= net_amount_cents)
);
CREATE INDEX product_settlements_seller_idx ON product_settlements(seller_id,created_at DESC,id DESC);
CREATE INDEX product_settlements_due_idx ON product_settlements(status,available_at,id)
  WHERE status IN ('pending_hold','available','transfer_pending');
CREATE INDEX product_settlements_payment_idx ON product_settlements(payment_id,status);

CREATE TABLE product_payout_batch_items (
  batch_id uuid NOT NULL REFERENCES product_payout_batches(id) ON DELETE CASCADE,
  settlement_id uuid NOT NULL UNIQUE REFERENCES product_settlements(id),
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','requested','succeeded','reconciliation_required','failed','cancelled')),
  provider_transfer_id text,
  error_code text,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(batch_id,settlement_id)
);
CREATE INDEX product_payout_batch_items_status_idx ON product_payout_batch_items(status,updated_at,settlement_id);

CREATE TABLE product_settlement_dispatches (
  settlement_id uuid PRIMARY KEY REFERENCES product_settlements(id) ON DELETE CASCADE,
  job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE product_settlement_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  settlement_id uuid NOT NULL REFERENCES product_settlements(id) ON DELETE CASCADE,
  event_type text NOT NULL CHECK (event_type ~ '^[a-z0-9_.-]{3,80}$'),
  from_status text,
  to_status text NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(settlement_id,event_type,created_at)
);
CREATE INDEX product_settlement_events_idx ON product_settlement_events(settlement_id,created_at,id);

CREATE FUNCTION reject_product_settlement_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'product settlement events are immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER product_settlement_events_immutable
BEFORE UPDATE OR DELETE ON product_settlement_events
FOR EACH ROW EXECUTE FUNCTION reject_product_settlement_mutation();

-- A settlement cannot be silently removed after a payment or a transfer has
-- been recorded.  The down migration therefore refuses to discard evidence.
