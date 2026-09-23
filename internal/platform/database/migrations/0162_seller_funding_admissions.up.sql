-- A review records a decision; admission consumes that exact decision for one
-- source job. Do not invent approvals for historical source transfers.
LOCK TABLE seller_payout_funding_dispatches IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE seller_payout_funding_admissions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 payout_request_id uuid NOT NULL UNIQUE REFERENCES seller_payout_requests(id),
 review_id uuid NOT NULL UNIQUE REFERENCES seller_payout_reviews(id),
 transfer_id uuid NOT NULL UNIQUE REFERENCES seller_payout_transfers(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 10 AND 1000),
 request_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(actor_id,idempotency_key)
);

CREATE FUNCTION assert_seller_funding_approval(source_id uuid, operator_id uuid, approval_id uuid) RETURNS void LANGUAGE plpgsql AS $$
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
  AND NOT EXISTS(SELECT 1 FROM seller_recovery_obligations WHERE seller_id=r.seller_id AND remaining_cents>0 AND status IN ('open','reconciliation_required'))
  AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
 ) THEN
  RAISE EXCEPTION 'funding requires the latest approval and its original available funds and bank' USING ERRCODE='23514';
 END IF;
END;
$$;

CREATE FUNCTION protect_seller_funding_admission() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'seller funding admission is immutable' USING ERRCODE='55000';
 END IF;
 PERFORM assert_seller_funding_approval(NEW.transfer_id,NEW.actor_id,NEW.review_id);
 IF NOT EXISTS(SELECT 1 FROM seller_payout_transfers t JOIN seller_payout_reviews v ON v.id=NEW.review_id
  WHERE t.id=NEW.transfer_id AND t.payout_request_id=NEW.payout_request_id AND v.payout_request_id=NEW.payout_request_id
  AND t.status='requested' AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_dispatches WHERE transfer_id=t.id)) THEN
  RAISE EXCEPTION 'funding admission must precede its dispatch registration' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER seller_funding_admission_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_funding_admissions
 FOR EACH ROW EXECUTE FUNCTION protect_seller_funding_admission();

CREATE FUNCTION require_seller_funding_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE admission seller_payout_funding_admissions%ROWTYPE;
BEGIN
 IF TG_OP='INSERT' OR (OLD.started_at IS NULL AND NEW.started_at IS NOT NULL) THEN
  IF current_setting('app.seller_funding_admission_protocol',true) IS DISTINCT FROM 'review-v1' THEN
   RAISE EXCEPTION 'funding writer must enforce review admission' USING ERRCODE='55000';
  END IF;
  SELECT * INTO admission FROM seller_payout_funding_admissions WHERE transfer_id=NEW.transfer_id;
  IF NOT FOUND THEN
   RAISE EXCEPTION 'source transfer has no approved admission' USING ERRCODE='23514';
  END IF;
  PERFORM assert_seller_funding_approval(NEW.transfer_id,admission.actor_id,admission.review_id);
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER seller_funding_approval_guard BEFORE INSERT OR UPDATE ON seller_payout_funding_dispatches
 FOR EACH ROW EXECUTE FUNCTION require_seller_funding_admission();

CREATE FUNCTION require_seller_funding_admission_job() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM seller_payout_funding_dispatches WHERE transfer_id=NEW.transfer_id) THEN
  RAISE EXCEPTION 'funding admission and dispatch must commit together' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER seller_funding_admission_job_guard AFTER INSERT ON seller_payout_funding_admissions
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_seller_funding_admission_job();
