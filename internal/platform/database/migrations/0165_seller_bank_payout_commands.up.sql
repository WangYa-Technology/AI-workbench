-- Freeze a separately authorized bank command after source funding succeeds.
-- This migration does not dispatch a bank payout or relax the confirmation
-- guard. Dispatch/observation and ledger consumption must consume this record.
CREATE TABLE seller_bank_payout_commands (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 payout_request_id uuid NOT NULL UNIQUE REFERENCES seller_payout_requests(id),
 source_transfer_id uuid NOT NULL UNIQUE REFERENCES seller_payout_transfers(id),
 funding_admission_id uuid NOT NULL UNIQUE REFERENCES seller_payout_funding_admissions(id),
 review_id uuid NOT NULL REFERENCES seller_payout_reviews(id),
 review_revision integer NOT NULL CHECK (review_revision>0),
 seller_id uuid NOT NULL REFERENCES users(id),
 settlement_id uuid NOT NULL UNIQUE REFERENCES product_settlements(id),
 payment_id uuid NOT NULL UNIQUE REFERENCES payment_intents(id),
 provider_identity jsonb NOT NULL CHECK (jsonb_typeof(provider_identity)='object'),
 destination_id text NOT NULL CHECK (destination_id ~ '^acct_[A-Za-z0-9_]{6,250}$'),
 bank_destination_id text NOT NULL CHECK (bank_destination_id ~ '^ba_[A-Za-z0-9_]{6,252}$'),
 source_provider_transfer_id text NOT NULL CHECK (source_provider_transfer_id ~ '^tr_[A-Za-z0-9_]{6,252}$'),
 amount_cents integer NOT NULL CHECK (amount_cents BETWEEN 1 AND 99999999),
 currency text NOT NULL CHECK (currency='USD'),
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 dispatch_key text NOT NULL UNIQUE,
 reason text NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 10 AND 1000),
 request_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 UNIQUE(actor_id,idempotency_key),
 CHECK (actor_id<>seller_id),
 CHECK (dispatch_key='seller-bank-payout-'||payout_request_id::text)
);
CREATE INDEX seller_bank_payout_commands_seller_idx ON seller_bank_payout_commands(seller_id,created_at,id);

-- Reusable before reservation and later before first dispatch. Call under
-- payment/order -> seller -> request locks; re-read eligibility after waits.
CREATE FUNCTION assert_seller_bank_payout_command(command seller_bank_payout_commands) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM product_settlements s JOIN payment_intents p ON p.id=s.payment_id
 JOIN orders o ON o.id=s.order_id WHERE s.id=command.settlement_id FOR UPDATE OF p,o;
 PERFORM pg_advisory_xact_lock(hashtextextended(command.seller_id::text,0));
 PERFORM 1 FROM seller_payout_requests WHERE id=command.payout_request_id FOR UPDATE;
 PERFORM 1 FROM product_settlements WHERE id=command.settlement_id FOR UPDATE;
 PERFORM 1 FROM users WHERE id=command.seller_id FOR SHARE;
 PERFORM 1 FROM payment_destinations WHERE user_id=command.seller_id AND provider='stripe' FOR SHARE;
 PERFORM 1 FROM product_settlement_settings WHERE singleton=true FOR SHARE;
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=command.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR command.actor_id=command.seller_id THEN
  RAISE EXCEPTION 'bank payout requires current independent finance authority' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM seller_payout_requests r
  JOIN seller_payout_transfers t ON t.payout_request_id=r.id
  JOIN seller_payout_funding_dispatches d ON d.transfer_id=t.id
  JOIN seller_payout_funding_admissions f ON f.transfer_id=t.id AND f.payout_request_id=r.id
  JOIN seller_payout_reviews v ON v.id=f.review_id AND v.payout_request_id=r.id
  JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id AND b.seller_id=r.seller_id
  JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.settlement_id=t.settlement_id
  JOIN product_settlements s ON s.id=t.settlement_id AND s.seller_id=r.seller_id
  JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id
  JOIN product_sale_owners own ON own.order_id=o.id AND own.seller_id=r.seller_id::text
  JOIN product_checkout_requests original ON original.payment_id=p.id
  JOIN users seller ON seller.id=r.seller_id
  JOIN payment_destinations pd ON pd.user_id=r.seller_id AND pd.provider='stripe'
  WHERE r.id=command.payout_request_id AND r.seller_id=command.seller_id
  AND r.status IN ('processing','reconciliation_required') AND seller.status='active'
  AND t.id=command.source_transfer_id AND t.status='succeeded' AND t.provider='stripe'
  AND t.provider_transfer_id=command.source_provider_transfer_id AND d.started_at IS NOT NULL
  AND f.id=command.funding_admission_id AND v.id=command.review_id AND v.revision=command.review_revision
  AND v.decision='approved' AND v.actor_id<>r.seller_id
  AND NOT EXISTS(SELECT 1 FROM seller_payout_reviews later WHERE later.payout_request_id=r.id AND later.revision>v.revision)
  AND s.id=command.settlement_id AND p.id=command.payment_id AND p.purpose='product'
  AND s.status='available' AND s.available_at<=clock_timestamp() AND s.provider='stripe'
  AND p.status='paid' AND o.status='fulfilled' AND p.provider=s.provider AND p.live_mode=s.live_mode
  AND p.order_id=o.id AND p.resource_id=o.product_id AND p.payer_id=o.buyer_id AND p.payee_id=r.seller_id
  AND p.amount_cents=o.amount_cents AND p.amount_cents=s.gross_amount_cents AND p.currency=o.currency
  AND d.payment_id=p.id AND d.provider_charge_id=p.provider_charge_id
  AND a.released_at IS NULL AND a.amount_cents=command.amount_cents
  AND r.amount_cents=command.amount_cents AND t.amount_cents=command.amount_cents AND s.net_amount_cents=command.amount_cents
  AND b.amount_cents=command.amount_cents AND v.amount_cents=command.amount_cents
  AND b.settlement_id=s.id AND b.payment_id=p.id AND v.settlement_id=s.id
  AND r.currency=command.currency AND t.currency=command.currency AND s.currency=command.currency
  AND b.currency=command.currency AND v.currency=command.currency AND p.currency=command.currency
  AND t.provider_identity=command.provider_identity AND b.provider_identity=command.provider_identity
  AND original.identity=command.provider_identity AND command.provider_identity->>'provider'='stripe'
  AND command.provider_identity->'liveMode'=to_jsonb(s.live_mode) AND t.live_mode=s.live_mode
  AND t.destination_id=command.destination_id AND b.destination_id=command.destination_id
  AND b.bank_destination_id=command.bank_destination_id AND v.bank_destination_id=command.bank_destination_id
  AND pd.destination_id=command.destination_id AND pd.status='verified' AND pd.charges_enabled AND pd.payouts_enabled
  AND pd.original_merchant_id=command.provider_identity->>'merchantId'
  AND COALESCE(pd.original_store_id,'')=COALESCE(command.provider_identity->>'storeId','')
  AND pd.original_live_mode=s.live_mode AND pd.original_endpoint=command.provider_identity->>'endpoint'
  AND pd.original_api_version=command.provider_identity->>'apiVersion'
  AND pd.original_request_version=command.provider_identity->>'requestVersion'
  AND EXISTS(SELECT 1 FROM product_settlement_settings WHERE singleton=true AND payout_mode='seller_payout')
  AND NOT seller_funds_recovery_blocks(r.seller_id,s.id)
  AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
  AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers other WHERE other.payout_request_id=r.id AND other.id<>t.id)
  AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_reads WHERE transfer_id=t.id AND (finished_at IS NULL OR requires_review))
 ) THEN
  RAISE EXCEPTION 'bank payout command conflicts with funded source, approval or reserved funds' USING ERRCODE='23514';
 END IF;
END;
$$;

CREATE FUNCTION protect_seller_bank_payout_command() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'seller bank payout command is immutable' USING ERRCODE='55000';
 END IF;
 IF current_setting('app.seller_bank_payout_protocol',true) IS DISTINCT FROM 'command-v1'
 OR NEW.created_at>clock_timestamp() OR NEW.created_at<clock_timestamp()-interval '1 minute' THEN
  RAISE EXCEPTION 'seller bank payout command protocol or timestamp invalid' USING ERRCODE='23514';
 END IF;
 PERFORM assert_seller_bank_payout_command(NEW);
 RETURN NEW;
END;
$$;
CREATE TRIGGER seller_bank_payout_command_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_bank_payout_commands
FOR EACH ROW EXECUTE FUNCTION protect_seller_bank_payout_command();
