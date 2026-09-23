-- Source funding is a platform-to-Connect transfer, never a bank payout.
-- Existing rows are not adopted: no job or original charge may be invented.
-- Hold writers out until the success guard is installed in this transaction.
LOCK TABLE seller_payout_requests IN SHARE ROW EXCLUSIVE MODE;
DO $$
BEGIN
  -- The new guard cannot prove how an old terminal request reached success.
  -- Refuse the upgrade until finance has reconciled those rows explicitly.
  IF EXISTS (SELECT 1 FROM seller_payout_requests WHERE status='succeeded') THEN
    RAISE EXCEPTION 'existing seller payout success requires reconciliation before migration' USING ERRCODE='55000';
  END IF;
END;
$$;

CREATE TABLE seller_payout_funding_dispatches (
  transfer_id uuid PRIMARY KEY REFERENCES seller_payout_transfers(id),
  job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
  payment_id uuid NOT NULL UNIQUE REFERENCES payment_intents(id),
  provider_charge_id text NOT NULL CHECK (provider_charge_id LIKE 'ch\_%' ESCAPE '\'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  started_at timestamptz,
  CHECK (started_at IS NULL OR started_at>=created_at)
);

CREATE FUNCTION protect_seller_payout_funding_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'seller funding dispatch is immutable' USING ERRCODE='55000';
  END IF;
  IF TG_OP='UPDATE' THEN
    IF ROW(NEW.transfer_id,NEW.job_id,NEW.payment_id,NEW.provider_charge_id,NEW.created_at)
       IS DISTINCT FROM ROW(OLD.transfer_id,OLD.job_id,OLD.payment_id,OLD.provider_charge_id,OLD.created_at)
       OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at)
    THEN
      RAISE EXCEPTION 'seller funding dispatch is immutable' USING ERRCODE='55000';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.started_at IS NOT NULL OR NOT EXISTS (
    SELECT 1 FROM seller_payout_transfers t
    JOIN seller_payout_requests r ON r.id=t.payout_request_id
    JOIN product_settlements s ON s.id=t.settlement_id
    JOIN payment_intents p ON p.id=s.payment_id
    JOIN orders o ON o.id=s.order_id
    JOIN product_checkout_requests c ON c.payment_id=p.id
    JOIN jobs j ON j.id=NEW.job_id
    WHERE t.id=NEW.transfer_id AND t.status='requested' AND t.provider='stripe'
      AND r.status IN ('requested','under_review') AND r.seller_id=s.seller_id
      AND s.status='available' AND s.available_at<=clock_timestamp()
      AND p.id=NEW.payment_id AND p.provider_charge_id=NEW.provider_charge_id
      AND p.purpose='product' AND p.status='paid' AND o.status='fulfilled'
      AND p.order_id=o.id AND p.payer_id=o.buyer_id AND p.resource_id=o.product_id
      AND p.payee_id=s.seller_id AND p.provider=t.provider AND p.live_mode=t.live_mode
      AND p.amount_cents=s.gross_amount_cents AND p.amount_cents=o.amount_cents
      AND p.currency=t.currency AND p.currency=o.currency
      AND c.identity=t.provider_identity AND t.dispatch_key='transfer-'||p.id::text
      AND j.kind='payment.fund_seller_payout' AND j.payload=jsonb_build_object('transferId',t.id)
      AND j.status='queued'
      AND EXISTS (SELECT 1 FROM product_sale_owners own WHERE own.order_id=o.id AND own.seller_id=s.seller_id::text)
      AND EXISTS (SELECT 1 FROM seller_payout_request_allocations a WHERE a.payout_request_id=r.id
                  AND a.settlement_id=s.id AND a.released_at IS NULL AND a.amount_cents=t.amount_cents)
      AND NOT EXISTS (SELECT 1 FROM product_settlement_dispatches d WHERE d.settlement_id=s.id AND d.reserved_at IS NOT NULL)
      AND NOT EXISTS (SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
      AND NOT EXISTS (SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
  ) THEN
    RAISE EXCEPTION 'seller funding job does not match original payment' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_funding_dispatch_guard
BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_funding_dispatches
FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_funding_dispatch();

-- Register reads before network I/O. An unfinished read remains visible after
-- process loss; a later read appends evidence rather than replacing it.
CREATE TABLE seller_payout_funding_reads (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_id uuid NOT NULL REFERENCES seller_payout_funding_dispatches(transfer_id),
  started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  finished_at timestamptz,
  outcome text,
  error_code text,
  requires_review boolean NOT NULL DEFAULT false,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  CHECK (((finished_at IS NULL AND outcome IS NULL AND error_code IS NULL AND NOT requires_review AND evidence='{}'::jsonb)
    OR (finished_at>=started_at AND outcome IN ('found','not_found','ambiguous','incomplete','error','reversed','skipped')
        AND jsonb_typeof(evidence)='object' AND evidence<>'{}'::jsonb)) IS TRUE)
);
CREATE INDEX seller_payout_funding_reads_transfer_idx ON seller_payout_funding_reads(transfer_id,started_at,id);

CREATE FUNCTION protect_seller_payout_funding_read() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'seller funding read evidence is immutable' USING ERRCODE='55000';
  END IF;
  IF TG_OP='INSERT' THEN
    IF NEW.finished_at IS NOT NULL THEN
      RAISE EXCEPTION 'seller funding read must be registered before completion' USING ERRCODE='23514';
    END IF;
  ELSIF ROW(NEW.id,NEW.transfer_id,NEW.started_at) IS DISTINCT FROM ROW(OLD.id,OLD.transfer_id,OLD.started_at)
     OR OLD.finished_at IS NOT NULL THEN
    RAISE EXCEPTION 'seller funding read evidence is immutable' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_funding_read_guard
BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_funding_reads
FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_funding_read();

-- No supported bank dispatch/confirmation exists yet. A successful source
-- transfer must not release the reservation or create another withdrawable credit.
CREATE FUNCTION reject_unconfirmed_seller_bank_payout() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status='succeeded' THEN
    RAISE EXCEPTION 'bank payout confirmation is not implemented' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_bank_confirmation_guard
BEFORE INSERT OR UPDATE ON seller_payout_requests
FOR EACH ROW EXECUTE FUNCTION reject_unconfirmed_seller_bank_payout();
