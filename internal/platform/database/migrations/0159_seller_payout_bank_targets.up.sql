-- Freeze a seller-confirmed, provider-verified bank on one payout request.
-- Existing requests have no inferred/default bank. This does not enable payout.
CREATE TABLE seller_payout_bank_targets (
  payout_request_id uuid PRIMARY KEY REFERENCES seller_payout_requests(id),
  seller_id uuid NOT NULL REFERENCES users(id),
  settlement_id uuid NOT NULL REFERENCES product_settlements(id),
  payment_id uuid NOT NULL REFERENCES payment_intents(id),
  provider_identity jsonb NOT NULL CHECK (jsonb_typeof(provider_identity)='object'),
  destination_id text NOT NULL CHECK (destination_id ~ '^acct_[A-Za-z0-9_]{6,250}$'),
  bank_destination_id text NOT NULL CHECK (bank_destination_id ~ '^ba_[A-Za-z0-9_]{6,252}$'),
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 99999999),
  currency text NOT NULL CHECK (currency='USD'),
  observed_at timestamptz NOT NULL CHECK (isfinite(observed_at)),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE FUNCTION protect_seller_payout_bank_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP<>'INSERT' THEN
    RAISE EXCEPTION 'seller payout bank evidence is immutable' USING ERRCODE='55000';
  END IF;
  -- Same payment/order -> seller -> request lock order as funding and refund.
  PERFORM 1 FROM product_settlements s JOIN payment_intents p ON p.id=s.payment_id
    JOIN orders o ON o.id=s.order_id AND p.order_id=o.id
    WHERE s.id=NEW.settlement_id FOR UPDATE OF p,o;
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.seller_id::text,0));
  PERFORM 1 FROM seller_payout_requests WHERE id=NEW.payout_request_id FOR UPDATE;
  PERFORM 1 FROM product_settlements WHERE id=NEW.settlement_id FOR UPDATE;
  PERFORM 1 FROM users WHERE id=NEW.seller_id FOR SHARE;
  PERFORM 1 FROM payment_destinations WHERE user_id=NEW.seller_id AND provider='stripe' FOR SHARE;
  PERFORM 1 FROM product_settlement_settings WHERE singleton=true FOR SHARE;
  IF NEW.observed_at>clock_timestamp() OR NEW.observed_at<clock_timestamp()-interval '1 minute'
     OR NOT isfinite(NEW.created_at) OR NEW.created_at<NEW.observed_at OR NEW.created_at>clock_timestamp()
     OR NOT EXISTS (
       SELECT 1 FROM seller_payout_requests r
       JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.released_at IS NULL
       JOIN product_settlements s ON s.id=a.settlement_id
       JOIN payment_intents p ON p.id=s.payment_id
       JOIN orders o ON o.id=s.order_id AND p.order_id=o.id
       JOIN product_checkout_requests original ON original.payment_id=p.id
       JOIN payment_destinations d ON d.provider='stripe' AND d.user_id=r.seller_id
       JOIN users u ON u.id=r.seller_id
       JOIN product_settlement_settings settings ON settings.singleton=true
       WHERE r.id=NEW.payout_request_id AND r.seller_id=NEW.seller_id AND u.status='active'
         AND r.status IN ('requested','under_review') AND settings.payout_mode='seller_payout'
         AND s.id=NEW.settlement_id AND s.payment_id=NEW.payment_id AND s.seller_id=r.seller_id
         AND s.status='available' AND s.available_at<=clock_timestamp() AND s.provider='stripe'
         AND p.status='paid' AND o.status='fulfilled'
         AND a.amount_cents=NEW.amount_cents AND s.net_amount_cents=NEW.amount_cents
         AND r.amount_cents=NEW.amount_cents AND r.currency=NEW.currency AND s.currency=NEW.currency
         AND original.identity=NEW.provider_identity AND NEW.provider_identity->>'provider'='stripe'
         AND NEW.provider_identity->'liveMode'=to_jsonb(s.live_mode)
         AND d.destination_id=NEW.destination_id AND d.status='verified' AND d.charges_enabled AND d.payouts_enabled
         AND d.original_merchant_id=NEW.provider_identity->>'merchantId'
         AND COALESCE(d.original_store_id,'')=COALESCE(NEW.provider_identity->>'storeId','')
         AND d.original_live_mode=s.live_mode AND d.original_endpoint=NEW.provider_identity->>'endpoint'
         AND d.original_api_version=NEW.provider_identity->>'apiVersion'
         AND d.original_request_version=NEW.provider_identity->>'requestVersion'
         AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=r.id)
         AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
         AND NOT EXISTS(SELECT 1 FROM seller_recovery_obligations WHERE seller_id=r.seller_id
           AND remaining_cents>0 AND status IN ('open','reconciliation_required'))
     ) THEN
    RAISE EXCEPTION 'seller payout bank does not match reserved funds and original identity' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_bank_target_guard
  BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_bank_targets
  FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_bank_target();

CREATE FUNCTION protect_seller_payout_source_bank_binding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bank seller_payout_bank_targets%ROWTYPE;
BEGIN
  -- Runs after the existing transfer evidence trigger has acquired the
  -- payment/order, seller and request locks; this statement sees committed
  -- bank selection after any wait. Historical unbound sources remain readable.
  SELECT * INTO bank FROM seller_payout_bank_targets WHERE payout_request_id=NEW.payout_request_id;
  IF FOUND AND ROW(bank.settlement_id,bank.provider_identity,bank.destination_id,bank.amount_cents,bank.currency)
    IS DISTINCT FROM ROW(NEW.settlement_id,NEW.provider_identity,NEW.destination_id,NEW.amount_cents,NEW.currency) THEN
    RAISE EXCEPTION 'seller payout source conflicts with frozen bank selection' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_transfer_z_bank_guard
  BEFORE INSERT ON seller_payout_transfers
  FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_source_bank_binding();
