-- Financial scope is derived only from immutable original/recovered payment
-- evidence. Current credentials/destination settings never rewrite the scope.
-- Drain API/worker money writers before applying; deploy readers together.
CREATE VIEW seller_funds_settlement_scopes AS
SELECT s.id AS settlement_id,s.seller_id,s.provider,s.live_mode,s.currency,
 CASE WHEN p.purpose='product' AND p.order_id=s.order_id AND p.payee_id=s.seller_id
   AND p.provider=s.provider AND p.live_mode=s.live_mode AND p.currency=s.currency
   AND p.amount_cents=s.gross_amount_cents AND o.id=p.order_id
   AND o.buyer_id=p.payer_id AND o.product_id=p.resource_id AND o.amount_cents=p.amount_cents AND o.currency=p.currency
   AND i.identity->>'provider'=s.provider AND i.identity->'liveMode'=to_jsonb(s.live_mode)
   AND jsonb_typeof(i.identity->'merchantId')='string' AND i.identity->>'merchantId'<>''
   AND jsonb_typeof(i.identity->'endpoint')='string' AND i.identity->>'endpoint'<>''
   AND (NOT i.identity ? 'storeId' OR jsonb_typeof(i.identity->'storeId')='string')
   AND CASE WHEN original.payment_id IS NOT NULL THEN
     original.request->>'PaymentID'=p.id::text AND original.request->>'Purpose'='product'
     AND original.request->>'OrderExternalID'=o.id::text AND original.request->>'ResourceID'=p.resource_id::text
     AND original.request->>'BuyerIdentity'=p.payer_id::text
     AND original.request->'AmountCents'=to_jsonb(p.amount_cents) AND original.request->>'Currency'=p.currency
   ELSE p.provider='stripe' AND recovered.binding->>'paymentId'=p.id::text
     AND recovered.binding->>'resourceId'=p.resource_id::text AND recovered.binding->>'orderId'=p.order_id::text
     AND recovered.binding->>'buyerId'=p.payer_id::text AND recovered.binding->'amountCents'=to_jsonb(p.amount_cents)
     AND recovered.binding->>'currency'=p.currency AND recovered.binding->'liveMode'=to_jsonb(p.live_mode)
     AND COALESCE(recovered.binding->>'providerCheckoutId','')=COALESCE(p.provider_checkout_id,'')
     AND (COALESCE(recovered.binding->>'providerPaymentId','')='' OR recovered.binding->>'providerPaymentId'=p.provider_payment_id)
     AND (COALESCE(recovered.binding->>'providerChargeId','')='' OR recovered.binding->>'providerChargeId'=p.provider_charge_id)
   END
 THEN encode(sha256(convert_to(jsonb_build_array('seller-funds-v1',s.seller_id,s.provider,s.live_mode,s.currency,
   i.identity->>'merchantId',COALESCE(i.identity->>'storeId',''),i.identity->>'endpoint')::text,'UTF8')),'hex')
 ELSE NULL END AS account_id
FROM product_settlements s LEFT JOIN payment_intents p ON p.id=s.payment_id
LEFT JOIN orders o ON o.id=s.order_id
LEFT JOIN product_checkout_requests original ON original.payment_id=p.id
LEFT JOIN product_payment_identity_recoveries recovered ON recovered.payment_id=p.id
CROSS JOIN LATERAL (SELECT CASE WHEN original.payment_id IS NOT NULL THEN original.identity ELSE recovered.identity END AS identity) i;

CREATE VIEW seller_funds_request_scopes AS
SELECT r.id AS payout_request_id,r.seller_id,r.status,r.amount_cents,r.currency,
 CASE WHEN r.seller_id=s.seller_id AND r.currency=s.currency AND a.amount_cents=r.amount_cents
   AND ps.net_amount_cents=r.amount_cents
   AND (SELECT count(*) FROM seller_payout_request_allocations WHERE payout_request_id=r.id)=1
 THEN s.account_id ELSE NULL END AS account_id
FROM seller_payout_requests r LEFT JOIN LATERAL (
 SELECT settlement_id,amount_cents FROM seller_payout_request_allocations WHERE payout_request_id=r.id ORDER BY settlement_id LIMIT 1
) a ON true
LEFT JOIN product_settlements ps ON ps.id=a.settlement_id
LEFT JOIN seller_funds_settlement_scopes s ON s.settlement_id=a.settlement_id;

CREATE VIEW seller_funds_ledger_scopes AS
SELECT e.id AS ledger_id,e.seller_id,e.entry_type,e.amount_cents,e.currency,e.settlement_id,e.payout_request_id,e.available_at,
 CASE WHEN e.entry_type='settlement_credit' AND e.seller_id=s.seller_id AND e.currency=s.currency AND e.amount_cents=ps.net_amount_cents
   THEN s.account_id
 WHEN e.entry_type IN ('payout_reservation','payout_release') AND e.seller_id=r.seller_id AND e.currency=r.currency AND e.amount_cents=r.amount_cents
   THEN r.account_id
 ELSE NULL END AS account_id
FROM seller_ledger_entries e LEFT JOIN product_settlements ps ON ps.id=e.settlement_id
LEFT JOIN seller_funds_settlement_scopes s ON s.settlement_id=e.settlement_id
LEFT JOIN seller_funds_request_scopes r ON r.payout_request_id=e.payout_request_id;

CREATE VIEW seller_funds_recovery_scopes AS
SELECT r.id AS recovery_id,r.seller_id,r.status,r.remaining_cents,r.currency,
 CASE WHEN r.seller_id=s.seller_id AND r.currency=s.currency THEN s.account_id ELSE NULL END AS account_id
FROM seller_recovery_obligations r LEFT JOIN seller_funds_settlement_scopes s ON s.settlement_id=r.settlement_id;

-- Unknown evidence is not pooled into any known merchant or environment.
-- A new, not-yet-allocated request has no money reservation to classify.
CREATE VIEW seller_funds_unscoped AS
SELECT seller_id,'settlement'::text AS kind,settlement_id AS id FROM seller_funds_settlement_scopes WHERE account_id IS NULL
UNION ALL SELECT seller_id,'ledger',ledger_id FROM seller_funds_ledger_scopes WHERE account_id IS NULL
UNION ALL SELECT seller_id,'recovery',recovery_id FROM seller_funds_recovery_scopes WHERE account_id IS NULL
UNION ALL SELECT r.seller_id,'payout',r.payout_request_id FROM seller_funds_request_scopes r WHERE r.account_id IS NULL
 AND (EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=r.payout_request_id)
      OR EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=r.payout_request_id));

CREATE FUNCTION seller_funds_recovery_blocks(owner_id uuid, target_settlement uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM seller_funds_settlement_scopes WHERE settlement_id=target_settlement AND seller_id=owner_id AND account_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM seller_funds_unscoped WHERE seller_id=owner_id)
 OR EXISTS(SELECT 1 FROM seller_funds_recovery_scopes r JOIN seller_funds_settlement_scopes s ON s.account_id=r.account_id
  WHERE s.settlement_id=target_settlement AND s.seller_id=owner_id AND r.seller_id=owner_id
  AND r.remaining_cents>0 AND r.status IN ('open','reconciliation_required'));
$$;

CREATE OR REPLACE FUNCTION protect_seller_payout_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
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
     OR seller_funds_recovery_blocks(request.seller_id,settlement.id)
     OR NOT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE settlement_id=settlement.id
                   AND seller_id=request.seller_id AND entry_type='settlement_credit'
                   AND amount_cents=NEW.amount_cents AND currency=request.currency AND available_at<=clock_timestamp())
  THEN
    RAISE EXCEPTION 'seller payout allocation does not match available funds' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION protect_seller_payout_transfer_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
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
     OR seller_funds_recovery_blocks(request.seller_id,settlement.id)
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

CREATE OR REPLACE FUNCTION protect_seller_payout_bank_target() RETURNS trigger LANGUAGE plpgsql AS $$
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
         AND NOT seller_funds_recovery_blocks(r.seller_id,s.id)
     ) THEN
    RAISE EXCEPTION 'seller payout bank does not match reserved funds and original identity' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION protect_seller_payout_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE seller uuid; current_revision integer; prior_decision text;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'seller payout review evidence is immutable' USING ERRCODE='55000';
 END IF;
 -- Payment/order -> seller -> request; the same order as application review,
 -- bank binding and refunds. Never acquire an actor lock ahead of these locks.
 PERFORM 1 FROM product_settlements s JOIN payment_intents p ON p.id=s.payment_id
 JOIN orders o ON o.id=s.order_id AND p.order_id=o.id WHERE s.id=NEW.settlement_id FOR UPDATE OF p,o;
 SELECT seller_id INTO STRICT seller FROM seller_payout_requests WHERE id=NEW.payout_request_id;
 PERFORM pg_advisory_xact_lock(hashtextextended(seller::text,0));
 PERFORM 1 FROM seller_payout_requests WHERE id=NEW.payout_request_id FOR UPDATE;
 IF NEW.decision='approved' THEN
  PERFORM 1 FROM product_settlements WHERE id=NEW.settlement_id FOR UPDATE;
  PERFORM 1 FROM users WHERE id=seller FOR SHARE;
  PERFORM 1 FROM payment_destinations WHERE provider='stripe' AND user_id=seller FOR SHARE;
  PERFORM 1 FROM product_settlement_settings WHERE singleton=true FOR SHARE;
  IF NOT EXISTS (
   SELECT 1 FROM seller_payout_bank_targets b JOIN product_settlements s ON s.id=b.settlement_id
   JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id AND p.order_id=o.id
   JOIN users u ON u.id=s.seller_id JOIN payment_destinations d ON d.provider='stripe' AND d.user_id=u.id
   JOIN product_checkout_requests original ON original.payment_id=p.id
   WHERE b.payout_request_id=NEW.payout_request_id AND u.status='active'
   AND s.status='available' AND s.available_at<=clock_timestamp() AND p.status='paid' AND o.status='fulfilled'
   AND d.status='verified' AND d.charges_enabled AND d.payouts_enabled AND d.destination_id=b.destination_id
   AND b.provider_identity=original.identity AND d.original_merchant_id=b.provider_identity->>'merchantId'
   AND COALESCE(d.original_store_id,'')=COALESCE(b.provider_identity->>'storeId','')
   AND d.original_live_mode=s.live_mode AND d.original_endpoint=b.provider_identity->>'endpoint'
   AND d.original_api_version=b.provider_identity->>'apiVersion' AND d.original_request_version=b.provider_identity->>'requestVersion'
   AND EXISTS(SELECT 1 FROM product_settlement_settings WHERE singleton=true AND payout_mode='seller_payout')
   AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
   AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
   AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
   AND NOT seller_funds_recovery_blocks(s.seller_id,s.id)
  ) THEN
   RAISE EXCEPTION 'seller payout approval requires current bank and funds authority' USING ERRCODE='23514';
  END IF;
 END IF;
 SELECT revision,decision INTO current_revision,prior_decision FROM seller_payout_reviews
 WHERE payout_request_id=NEW.payout_request_id ORDER BY revision DESC LIMIT 1;
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=NEW.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR NEW.actor_id=seller OR NEW.revision<>COALESCE(current_revision,0)+1
 OR (prior_decision='approved' AND NEW.decision='approved')
 OR NOT EXISTS (
  SELECT 1 FROM seller_payout_requests r
  JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.released_at IS NULL
  JOIN product_settlements s ON s.id=a.settlement_id
  LEFT JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id
  WHERE r.id=NEW.payout_request_id AND r.status IN ('requested','under_review')
  AND a.settlement_id=NEW.settlement_id AND s.seller_id=r.seller_id
  AND r.amount_cents=NEW.amount_cents AND a.amount_cents=NEW.amount_cents AND r.currency=NEW.currency
  AND COALESCE(b.bank_destination_id,'')=NEW.bank_destination_id
  AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers t WHERE t.payout_request_id=r.id)
 ) THEN
  RAISE EXCEPTION 'seller payout review conflicts with authority or request' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION assert_seller_funding_approval(source_id uuid, operator_id uuid, approval_id uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE seller uuid; settlement uuid;
BEGIN
 SELECT s.id,s.seller_id INTO STRICT settlement,seller FROM seller_payout_transfers t
 JOIN product_settlements s ON s.id=t.settlement_id WHERE t.id=source_id;
 -- Same serialization order as review, refund and cancellation.
 PERFORM 1 FROM product_settlements s JOIN payment_intents p ON p.id=s.payment_id
 JOIN orders o ON o.id=s.order_id WHERE s.id=settlement FOR UPDATE OF p,o;
 PERFORM pg_advisory_xact_lock(hashtextextended(seller::text,0));
 PERFORM 1 FROM seller_payout_requests r JOIN seller_payout_transfers t ON t.payout_request_id=r.id
 WHERE t.id=source_id FOR UPDATE OF r;
 PERFORM 1 FROM product_settlements WHERE id=settlement FOR UPDATE;
 PERFORM 1 FROM users WHERE id=seller FOR SHARE;
 PERFORM 1 FROM payment_destinations WHERE user_id=seller AND provider='stripe' FOR SHARE;
 PERFORM 1 FROM product_settlement_settings WHERE singleton=true FOR SHARE;
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=operator_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR operator_id=seller THEN
  RAISE EXCEPTION 'funding admission requires current independent finance authority' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM seller_payout_transfers t
  JOIN seller_payout_requests r ON r.id=t.payout_request_id
  JOIN seller_payout_reviews v ON v.id=approval_id AND v.payout_request_id=r.id
  JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id AND b.seller_id=r.seller_id
  JOIN product_settlements s ON s.id=t.settlement_id AND s.seller_id=r.seller_id
  JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.settlement_id=s.id
  JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id AND p.order_id=o.id
  JOIN product_checkout_requests original ON original.payment_id=p.id
  JOIN users u ON u.id=r.seller_id
  JOIN payment_destinations d ON d.user_id=r.seller_id AND d.provider='stripe'
  WHERE t.id=source_id AND t.status IN ('requested','processing')
  AND r.status IN ('requested','under_review','processing') AND u.status='active'
  AND v.decision='approved' AND v.actor_id<>r.seller_id
  AND NOT EXISTS(SELECT 1 FROM seller_payout_reviews newer WHERE newer.payout_request_id=r.id AND newer.revision>v.revision)
  AND v.settlement_id=s.id AND b.settlement_id=s.id AND b.payment_id=p.id
  AND v.bank_destination_id=b.bank_destination_id
  AND v.amount_cents=t.amount_cents AND b.amount_cents=t.amount_cents
  AND r.amount_cents=t.amount_cents AND a.amount_cents=t.amount_cents AND s.net_amount_cents=t.amount_cents
  AND a.released_at IS NULL AND s.status='available' AND s.available_at<=clock_timestamp()
  AND p.status='paid' AND o.status='fulfilled'
  AND v.currency=t.currency AND b.currency=t.currency AND r.currency=t.currency AND s.currency=t.currency
  AND t.provider='stripe' AND t.live_mode=s.live_mode
  AND b.destination_id=t.destination_id AND b.provider_identity=t.provider_identity AND original.identity=t.provider_identity
  AND d.destination_id=t.destination_id AND d.status='verified' AND d.charges_enabled AND d.payouts_enabled
  AND d.original_merchant_id=t.provider_identity->>'merchantId'
  AND COALESCE(d.original_store_id,'')=COALESCE(t.provider_identity->>'storeId','')
  AND d.original_live_mode=t.live_mode AND d.original_endpoint=t.provider_identity->>'endpoint'
  AND d.original_api_version=t.provider_identity->>'apiVersion' AND d.original_request_version=t.provider_identity->>'requestVersion'
  AND EXISTS(SELECT 1 FROM product_settlement_settings WHERE singleton=true AND payout_mode='seller_payout')
  AND NOT seller_funds_recovery_blocks(r.seller_id,s.id)
  AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
 ) THEN
  RAISE EXCEPTION 'funding requires the latest approval and its original available funds and bank' USING ERRCODE='23514';
 END IF;
END;
$$;
