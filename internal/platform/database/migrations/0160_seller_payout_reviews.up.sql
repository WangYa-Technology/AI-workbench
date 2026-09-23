-- Financial review is distinct from source funding and bank payout. No job is
-- admitted by this migration; an approved review does not prove funds moved.
CREATE TABLE seller_payout_reviews (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 payout_request_id uuid NOT NULL REFERENCES seller_payout_requests(id),
 revision integer NOT NULL CHECK (revision>0),
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 decision text NOT NULL CHECK (decision IN ('approved','rejected')),
 reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 10 AND 1000),
 settlement_id uuid NOT NULL REFERENCES product_settlements(id),
 amount_cents integer NOT NULL CHECK (amount_cents>0),
 currency text NOT NULL CHECK (currency='USD'),
 bank_destination_id text NOT NULL,
 request_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (actor_id,idempotency_key),
 UNIQUE (payout_request_id,revision),
 CHECK (bank_destination_id='' OR bank_destination_id ~ '^ba_[A-Za-z0-9_]{6,252}$'),
 CHECK (decision<>'approved' OR bank_destination_id<>'')
);
CREATE INDEX seller_payout_requests_review_directory_idx ON seller_payout_requests(created_at DESC,id DESC);

CREATE FUNCTION protect_seller_payout_review() RETURNS trigger LANGUAGE plpgsql AS $$
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
   AND NOT EXISTS(SELECT 1 FROM seller_recovery_obligations WHERE seller_id=s.seller_id AND remaining_cents>0 AND status IN ('open','reconciliation_required'))
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
CREATE TRIGGER seller_payout_review_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_payout_reviews
 FOR EACH ROW EXECUTE FUNCTION protect_seller_payout_review();
