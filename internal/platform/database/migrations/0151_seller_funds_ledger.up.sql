-- Independent seller funds ledger. This is deliberately separate from the
-- local wallet ledger and from product_settlements (which is an immutable
-- payment settlement snapshot, not a withdrawable balance).
CREATE TABLE seller_payout_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id uuid NOT NULL REFERENCES users(id),
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL CHECK (currency='USD'),
  idempotency_key text NOT NULL,
  status text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested','under_review','cancelled','processing','succeeded','failed','reconciliation_required')),
  failure_code text,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(seller_id,idempotency_key)
);
CREATE INDEX seller_payout_requests_seller_idx ON seller_payout_requests(seller_id,created_at DESC,id DESC);

CREATE TABLE seller_payout_request_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  payout_request_id uuid NOT NULL REFERENCES seller_payout_requests(id),
  event_type text NOT NULL CHECK (event_type ~ '^[a-z0-9_.-]{3,80}$'),
  from_status text,
  to_status text NOT NULL,
  event_key text NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(payout_request_id,event_key)
);
CREATE INDEX seller_payout_request_events_idx ON seller_payout_request_events(payout_request_id,created_at,id);

CREATE FUNCTION reject_seller_payout_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'seller payout request events are immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER seller_payout_request_events_immutable
BEFORE UPDATE OR DELETE ON seller_payout_request_events
FOR EACH ROW EXECUTE FUNCTION reject_seller_payout_event_mutation();

CREATE TABLE seller_ledger_entries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id uuid NOT NULL REFERENCES users(id),
  entry_type text NOT NULL CHECK (entry_type IN ('settlement_credit','recovery_debit','payout_reservation','payout_release','adjustment')),
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL CHECK (currency='USD'),
  settlement_id uuid REFERENCES product_settlements(id),
  payout_request_id uuid REFERENCES seller_payout_requests(id),
  idempotency_key text NOT NULL UNIQUE,
  available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK ((entry_type='settlement_credit') = (settlement_id IS NOT NULL)),
  CHECK ((entry_type IN ('payout_reservation','payout_release')) = (payout_request_id IS NOT NULL))
);
CREATE INDEX seller_ledger_balance_idx ON seller_ledger_entries(seller_id,currency,available_at,created_at,id);
CREATE UNIQUE INDEX seller_ledger_settlement_credit_uq ON seller_ledger_entries(settlement_id)
  WHERE entry_type='settlement_credit';
CREATE UNIQUE INDEX seller_ledger_payout_reservation_uq ON seller_ledger_entries(payout_request_id)
  WHERE entry_type='payout_reservation';

CREATE TABLE seller_recovery_obligations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id uuid NOT NULL REFERENCES users(id),
  settlement_id uuid NOT NULL UNIQUE REFERENCES product_settlements(id),
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  remaining_cents integer NOT NULL CHECK (remaining_cents >= 0 AND remaining_cents <= amount_cents),
  currency text NOT NULL CHECK (currency='USD'),
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','settled','written_off','reconciliation_required')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX seller_recovery_obligations_seller_idx ON seller_recovery_obligations(seller_id,status,created_at,id);

CREATE TABLE seller_funds_reconciliations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id uuid REFERENCES users(id),
  scope text NOT NULL CHECK (scope IN ('seller','global')),
  status text NOT NULL CHECK (status IN ('open','resolved','rejected')),
  reason text NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  resolved_at timestamptz
);

CREATE FUNCTION reject_seller_ledger_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'seller ledger entries are immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER seller_ledger_entries_immutable
BEFORE UPDATE OR DELETE ON seller_ledger_entries
FOR EACH ROW EXECUTE FUNCTION reject_seller_ledger_mutation();

CREATE FUNCTION protect_seller_payout_request_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(NEW.id,NEW.seller_id,NEW.amount_cents,NEW.currency,NEW.idempotency_key,NEW.created_at)
     IS DISTINCT FROM ROW(OLD.id,OLD.seller_id,OLD.amount_cents,OLD.currency,OLD.idempotency_key,OLD.created_at)
  THEN RAISE EXCEPTION 'seller payout request identity is immutable'; END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_request_identity_guard
BEFORE UPDATE ON seller_payout_requests
FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_request_identity();

CREATE FUNCTION seller_payout_request_event_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM seller_payout_requests r WHERE r.id=NEW.payout_request_id
    AND (NEW.from_status IS NULL OR r.status=NEW.to_status OR NEW.to_status='requested')) THEN
    RAISE EXCEPTION 'seller payout event must reference a known request';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_request_event_guard
BEFORE INSERT ON seller_payout_request_events
FOR EACH ROW EXECUTE FUNCTION seller_payout_request_event_guard();

-- Preserve still-unpaid historical settlements and outstanding recovery debt.
INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,settlement_id,idempotency_key,available_at,evidence)
SELECT seller_id,'settlement_credit',net_amount_cents,currency,id,
       'settlement-credit:'||id::text,available_at,jsonb_build_object('status',status)
FROM product_settlements
WHERE status IN ('pending_hold','available') AND net_amount_cents > 0
ON CONFLICT DO NOTHING;

INSERT INTO seller_recovery_obligations(seller_id,settlement_id,amount_cents,remaining_cents,currency,status)
SELECT seller_id,id,recovery_amount_cents,recovery_amount_cents,currency,'open'
FROM product_settlements
WHERE recovery_amount_cents > 0
ON CONFLICT (settlement_id) DO NOTHING;
