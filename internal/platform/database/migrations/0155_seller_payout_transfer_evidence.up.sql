-- There has not been a supported dispatcher for these rows. Do not invent
-- original merchant/dispatch evidence for pre-existing financial records.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM seller_payout_transfers) THEN
    RAISE EXCEPTION 'seller payout transfer evidence requires reconciliation before migration' USING ERRCODE='55000';
  END IF;
END;
$$;

ALTER TABLE seller_payout_transfers
  ADD COLUMN provider_identity jsonb NOT NULL,
  ADD COLUMN dispatch_key text NOT NULL UNIQUE CHECK (length(dispatch_key) BETWEEN 8 AND 160),
  ADD COLUMN reserved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  ADD CONSTRAINT seller_payout_transfer_identity_shape CHECK ((
    jsonb_typeof(provider_identity)='object'
    AND provider_identity->>'provider'=provider
    AND provider_identity->'liveMode'=to_jsonb(live_mode)
    AND length(provider_identity->>'merchantId')>0
    AND length(provider_identity->>'endpoint')>0
    AND length(provider_identity->>'apiVersion')>0
    AND length(provider_identity->>'requestVersion')>0
  ) IS TRUE),
  ADD CONSTRAINT seller_payout_transfer_result_shape CHECK (
    jsonb_typeof(evidence)='object'
    AND (provider_transfer_id IS NULL OR length(btrim(provider_transfer_id))>0)
    AND (status<>'succeeded' OR (provider_transfer_id IS NOT NULL AND evidence<>'{}'::jsonb))
    AND (status<>'failed' OR (provider_transfer_id IS NULL AND error_code IS NOT NULL AND evidence<>'{}'::jsonb))
  );

CREATE FUNCTION protect_seller_payout_transfer_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  request seller_payout_requests%ROWTYPE;
  settlement product_settlements%ROWTYPE;
  mode text;
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'seller payout transfer evidence is immutable' USING ERRCODE='55000';
  END IF;
  IF TG_OP='UPDATE' THEN
    IF ROW(NEW.provider_identity,NEW.dispatch_key,NEW.reserved_at)
       IS DISTINCT FROM ROW(OLD.provider_identity,OLD.dispatch_key,OLD.reserved_at)
       OR (OLD.provider_transfer_id IS NOT NULL AND NEW.provider_transfer_id IS DISTINCT FROM OLD.provider_transfer_id)
       OR NOT NEW.evidence @> OLD.evidence
       OR (OLD.status IN ('succeeded','failed') AND
           ROW(NEW.status,NEW.provider_transfer_id,NEW.error_code,NEW.evidence)
           IS DISTINCT FROM ROW(OLD.status,OLD.provider_transfer_id,OLD.error_code,OLD.evidence))
       OR (OLD.status<>'requested' AND NEW.status='requested')
       OR (OLD.status='reconciliation_required' AND NEW.status='processing')
    THEN
      RAISE EXCEPTION 'seller payout dispatch or result evidence is immutable' USING ERRCODE='55000';
    END IF;
    RETURN NEW;
  END IF;

  -- Serialize with application cancellation and refund writers before reading
  -- current request/settlement state. Never acquire a payment lock after this.
  SELECT * INTO STRICT request FROM seller_payout_requests WHERE id=NEW.payout_request_id;
  PERFORM pg_advisory_xact_lock(hashtextextended(request.seller_id::text,0));
  SELECT * INTO STRICT request FROM seller_payout_requests WHERE id=NEW.payout_request_id FOR UPDATE;
  SELECT payout_mode INTO STRICT mode FROM product_settlement_settings WHERE singleton=true FOR SHARE;
  SELECT * INTO STRICT settlement FROM product_settlements WHERE id=NEW.settlement_id FOR UPDATE;
  IF mode<>'seller_payout' OR request.status NOT IN ('requested','under_review')
     OR NEW.status<>'requested' OR NEW.provider_transfer_id IS NOT NULL OR NEW.error_code IS NOT NULL
     OR NEW.evidence<>'{}'::jsonb OR NEW.reserved_at>clock_timestamp() OR NEW.reserved_at<NEW.created_at
     OR settlement.status<>'available' OR settlement.available_at>clock_timestamp()
     OR settlement.seller_id<>request.seller_id OR settlement.provider<>NEW.provider
     OR settlement.live_mode<>NEW.live_mode OR settlement.currency<>NEW.currency
     OR request.currency<>NEW.currency OR settlement.net_amount_cents<>NEW.amount_cents
     OR request.amount_cents<>NEW.amount_cents
     OR EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=settlement.id AND reserved_at IS NOT NULL)
     OR EXISTS(SELECT 1 FROM seller_recovery_obligations WHERE seller_id=request.seller_id
               AND remaining_cents>0 AND status IN ('open','reconciliation_required'))
     OR NOT EXISTS(SELECT 1 FROM seller_payout_request_allocations WHERE payout_request_id=request.id
                   AND settlement_id=settlement.id AND amount_cents=NEW.amount_cents AND released_at IS NULL)
     OR NOT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=request.id
                   AND seller_id=request.seller_id AND entry_type='payout_reservation'
                   AND amount_cents=NEW.amount_cents AND currency=NEW.currency)
     OR NOT EXISTS(SELECT 1 FROM product_checkout_requests WHERE payment_id=settlement.payment_id AND identity=NEW.provider_identity)
     OR NOT EXISTS(SELECT 1 FROM payment_destinations d WHERE d.provider=NEW.provider AND d.user_id=request.seller_id
                   AND d.destination_id=NEW.destination_id AND d.status='verified' AND d.charges_enabled AND d.payouts_enabled
                   AND d.original_merchant_id=NEW.provider_identity->>'merchantId'
                   AND COALESCE(d.original_store_id,'')=COALESCE(NEW.provider_identity->>'storeId','')
                   AND d.original_live_mode=NEW.live_mode AND d.original_endpoint=NEW.provider_identity->>'endpoint'
                   AND d.original_api_version=NEW.provider_identity->>'apiVersion'
                   AND d.original_request_version=NEW.provider_identity->>'requestVersion')
  THEN
    RAISE EXCEPTION 'seller payout transfer does not match reserved funds and original identity' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_transfer_evidence_guard
BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_transfers
FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_transfer_evidence();

-- An unknown external result must not release its local reservation merely
-- because the parent request was marked failed or moved back to review.
CREATE FUNCTION protect_seller_payout_transfer_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id) AND (
    (NEW.status IN ('requested','under_review','cancelled') AND NEW.status<>OLD.status)
    OR (NEW.status='failed' AND EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id AND status<>'failed'))
    OR (NEW.status='succeeded' AND EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id AND status<>'succeeded'))
    OR (OLD.status IN ('failed','succeeded') AND NEW.status<>OLD.status)
    OR (OLD.status='reconciliation_required' AND NEW.status='processing')
  ) THEN
    RAISE EXCEPTION 'seller payout request conflicts with transfer evidence' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_transfer_request_guard
BEFORE UPDATE ON seller_payout_requests
FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_transfer_request();
