-- A product settlement must have exactly one external funds出口.  Keep the
-- existing automatic settlement worker as the default until the independent
-- seller payout dispatcher and its provider lookup/recovery path are enabled.
ALTER TABLE product_settlement_settings
  ADD COLUMN payout_mode text NOT NULL DEFAULT 'automatic'
  CHECK (payout_mode IN ('automatic','seller_payout'));

-- Manual payout requests reserve complete, still-available settlements.  The
-- allocation table makes the reservation auditable and prevents a request
-- from silently drawing the same settlement twice.
CREATE TABLE seller_payout_request_allocations (
  payout_request_id uuid NOT NULL REFERENCES seller_payout_requests(id) ON DELETE CASCADE,
  settlement_id uuid NOT NULL REFERENCES product_settlements(id),
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (payout_request_id, settlement_id),
  UNIQUE (settlement_id)
);
CREATE INDEX seller_payout_request_allocations_settlement_idx
  ON seller_payout_request_allocations(settlement_id);

-- Provider dispatch is deliberately not enabled by this migration.  This
-- table is the durable hand-off for the future dispatcher and lets recovery
-- tooling distinguish an unknown external result from a retryable request.
CREATE TABLE seller_payout_transfers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  payout_request_id uuid NOT NULL REFERENCES seller_payout_requests(id),
  settlement_id uuid NOT NULL REFERENCES product_settlements(id),
  provider text NOT NULL,
  live_mode boolean NOT NULL,
  destination_id text NOT NULL,
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL CHECK (currency='USD'),
  provider_transfer_id text,
  status text NOT NULL DEFAULT 'requested'
    CHECK (status IN ('requested','processing','succeeded','failed','reconciliation_required')),
  error_code text,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (payout_request_id, settlement_id),
  UNIQUE (provider, provider_transfer_id)
);
CREATE INDEX seller_payout_transfers_status_idx
  ON seller_payout_transfers(status,updated_at,id);

CREATE FUNCTION reject_seller_payout_transfer_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(NEW.id,NEW.payout_request_id,NEW.settlement_id,NEW.provider,NEW.live_mode,
         NEW.destination_id,NEW.amount_cents,NEW.currency,NEW.created_at)
     IS DISTINCT FROM ROW(OLD.id,OLD.payout_request_id,OLD.settlement_id,OLD.provider,OLD.live_mode,
         OLD.destination_id,OLD.amount_cents,OLD.currency,OLD.created_at)
  THEN
    RAISE EXCEPTION 'seller payout transfer identity is immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_transfer_identity_guard
BEFORE UPDATE ON seller_payout_transfers
FOR EACH ROW EXECUTE FUNCTION reject_seller_payout_transfer_mutation();
