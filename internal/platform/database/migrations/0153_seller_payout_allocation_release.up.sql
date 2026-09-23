-- Keep cancelled allocations as evidence while allowing their funds to be
-- reserved again. Only cancellation before provider dispatch can release one.
ALTER TABLE seller_payout_request_allocations ADD COLUMN released_at timestamptz;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM seller_payout_requests r
    JOIN seller_payout_transfers t ON t.payout_request_id=r.id
    WHERE r.status='cancelled'
  ) THEN
    RAISE EXCEPTION 'cancelled payout has transfer evidence; reconcile before migration' USING ERRCODE='55000';
  END IF;
END;
$$;

UPDATE seller_payout_request_allocations a SET released_at=clock_timestamp()
FROM seller_payout_requests r WHERE r.id=a.payout_request_id AND r.status='cancelled';

ALTER TABLE seller_payout_request_allocations
  DROP CONSTRAINT seller_payout_request_allocations_settlement_id_key;
CREATE UNIQUE INDEX seller_payout_allocation_active_uq
  ON seller_payout_request_allocations(settlement_id) WHERE released_at IS NULL;

CREATE FUNCTION protect_seller_payout_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  request seller_payout_requests%ROWTYPE;
  settlement product_settlements%ROWTYPE;
  mode text;
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'seller payout allocation evidence is immutable' USING ERRCODE='55000';
  END IF;
  SELECT * INTO STRICT request FROM seller_payout_requests WHERE id=NEW.payout_request_id;
  IF TG_OP='UPDATE' THEN
    IF ROW(NEW.payout_request_id,NEW.settlement_id,NEW.amount_cents,NEW.created_at)
       IS DISTINCT FROM ROW(OLD.payout_request_id,OLD.settlement_id,OLD.amount_cents,OLD.created_at)
       OR OLD.released_at IS NOT NULL OR NEW.released_at IS NULL
       OR NEW.released_at < OLD.created_at OR request.status<>'cancelled'
       OR EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=request.id)
    THEN
      RAISE EXCEPTION 'seller payout allocation cannot be changed or released' USING ERRCODE='55000';
    END IF;
    RETURN NEW;
  END IF;

  PERFORM pg_advisory_xact_lock(hashtextextended(request.seller_id::text,0));
  SELECT * INTO STRICT request FROM seller_payout_requests WHERE id=NEW.payout_request_id FOR UPDATE;
  SELECT payout_mode INTO STRICT mode FROM product_settlement_settings WHERE singleton=true FOR SHARE;
  SELECT * INTO STRICT settlement FROM product_settlements WHERE id=NEW.settlement_id FOR UPDATE;
  IF mode<>'seller_payout' OR NEW.released_at IS NOT NULL OR request.status NOT IN ('requested','under_review')
     OR settlement.seller_id<>request.seller_id OR settlement.currency<>request.currency
     OR settlement.status<>'available' OR settlement.available_at>clock_timestamp()
     OR settlement.net_amount_cents<>NEW.amount_cents OR request.amount_cents<>NEW.amount_cents
     OR EXISTS(SELECT 1 FROM seller_payout_request_allocations WHERE payout_request_id=request.id)
     OR EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=settlement.id AND reserved_at IS NOT NULL)
     OR EXISTS(SELECT 1 FROM seller_payout_transfers WHERE settlement_id=settlement.id)
     OR EXISTS(SELECT 1 FROM seller_recovery_obligations WHERE seller_id=request.seller_id
               AND remaining_cents>0 AND status IN ('open','reconciliation_required'))
     OR NOT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE settlement_id=settlement.id
                   AND seller_id=request.seller_id AND entry_type='settlement_credit'
                   AND amount_cents=NEW.amount_cents AND currency=request.currency AND available_at<=clock_timestamp())
  THEN
    RAISE EXCEPTION 'seller payout allocation does not match available funds' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_allocation_guard
BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_request_allocations
FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_allocation();

CREATE FUNCTION protect_seller_payout_cancellation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status='cancelled' AND OLD.status<>'cancelled' AND (
    OLD.status NOT IN ('requested','under_review')
    OR EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id)
  ) THEN
    RAISE EXCEPTION 'seller payout already dispatched or awaiting reconciliation' USING ERRCODE='55000';
  END IF;
  IF OLD.status='cancelled' AND NEW.status<>OLD.status THEN
    RAISE EXCEPTION 'cancelled seller payout is terminal' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_cancellation_guard
BEFORE UPDATE ON seller_payout_requests
FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_cancellation();

CREATE FUNCTION release_cancelled_seller_payout_allocations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE seller_payout_request_allocations SET released_at=clock_timestamp()
    WHERE payout_request_id=NEW.id AND released_at IS NULL;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_cancelled_allocations
AFTER UPDATE OF status ON seller_payout_requests
FOR EACH ROW WHEN (NEW.status='cancelled' AND OLD.status<>'cancelled')
EXECUTE FUNCTION release_cancelled_seller_payout_allocations();
