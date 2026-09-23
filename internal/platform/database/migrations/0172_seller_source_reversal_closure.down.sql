LOCK TABLE seller_source_reversal_closures IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_source_reversal_closures) THEN
  RAISE EXCEPTION 'source return consumption must be retained' USING ERRCODE='55000'; END IF;
END; $$;
DROP TRIGGER closed_source_settlement_writer ON product_settlements;
DROP FUNCTION protect_closed_source_settlement_writer();
DROP TRIGGER consumed_source_recovery_guard ON seller_recovery_obligations;
DROP FUNCTION protect_consumed_source_recovery();
DROP TRIGGER seller_source_release_ledger_guard ON seller_ledger_entries;
DROP FUNCTION protect_seller_source_release_ledger();
DROP INDEX seller_ledger_release_once;

CREATE OR REPLACE FUNCTION assert_seller_source_reversal_command(command seller_source_reversal_commands) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM lock_seller_payout_disposition(command.payout_request_id);
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=command.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR command.actor_id=command.seller_id THEN
  RAISE EXCEPTION 'reversal requires current independent finance authority' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM seller_payout_requests r JOIN seller_payout_transfers t ON t.payout_request_id=r.id
  JOIN seller_payout_funding_dispatches d ON d.transfer_id=t.id
  JOIN seller_payout_funding_admissions f ON f.transfer_id=t.id AND f.payout_request_id=r.id
  JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.settlement_id=t.settlement_id
  JOIN product_settlements s ON s.id=t.settlement_id
  JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id
  JOIN product_sale_owners own ON own.order_id=o.id AND own.seller_id=r.seller_id::text
  JOIN product_checkout_requests original ON original.payment_id=p.id
  CROSS JOIN LATERAL seller_source_reversal_bank_state(r.id) bank
  WHERE r.id=command.payout_request_id AND r.seller_id=command.seller_id
  AND r.status IN ('processing','reconciliation_required')
  AND t.id=command.source_transfer_id AND t.status='succeeded' AND t.provider='stripe'
  AND t.provider_transfer_id=command.provider_transfer_id AND t.provider_identity=command.provider_identity
  AND original.identity=command.provider_identity AND command.provider_identity->>'provider'='stripe'
  AND command.provider_identity->'liveMode'=to_jsonb(command.live_mode)
  AND t.destination_id=command.destination_id AND t.live_mode=command.live_mode
  AND t.amount_cents=command.amount_cents AND r.amount_cents=command.amount_cents
  AND a.released_at IS NULL AND a.amount_cents=command.amount_cents
  AND s.id=command.settlement_id AND s.seller_id=command.seller_id AND s.net_amount_cents=command.amount_cents
  AND s.provider='stripe' AND s.live_mode=command.live_mode AND p.id=command.payment_id AND p.purpose='product'
  AND p.order_id=o.id AND p.resource_id=o.product_id AND p.payer_id=o.buyer_id AND p.payee_id=r.seller_id
  AND p.amount_cents=o.amount_cents AND p.amount_cents=s.gross_amount_cents AND p.provider=s.provider
  AND p.live_mode=command.live_mode AND p.provider_charge_id=command.provider_charge_id
  AND d.started_at IS NOT NULL AND d.payment_id=p.id AND d.provider_charge_id=command.provider_charge_id
  AND r.currency=command.currency AND s.currency=command.currency AND t.currency=command.currency
  AND p.currency=command.currency AND o.currency=command.currency
  AND bank.command_id IS NOT DISTINCT FROM command.bank_command_id
  AND bank.result_id IS NOT DISTINCT FROM command.bank_result_id AND bank.disposition=command.bank_disposition
  AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
  AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_reads WHERE transfer_id=t.id AND (finished_at IS NULL OR requires_review))
  AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers other WHERE other.payout_request_id=r.id AND other.id<>t.id)
  AND EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=r.id AND entry_type='payout_reservation'
   AND seller_id=r.seller_id AND amount_cents=command.amount_cents AND currency=command.currency)
  AND NOT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=r.id AND entry_type='payout_release')
 ) THEN
  RAISE EXCEPTION 'reversal conflicts with source, reservation or bank evidence' USING ERRCODE='23514';
 END IF;
END; $$;
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
CREATE OR REPLACE FUNCTION protect_seller_payout_cancellation() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE OR REPLACE FUNCTION protect_seller_payout_transfer_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id) AND (
  (NEW.status IN ('requested','under_review','cancelled') AND NEW.status<>OLD.status)
  OR (NEW.status='failed' AND EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id AND status<>'failed'))
  OR (NEW.status='succeeded' AND EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=OLD.id AND status<>'succeeded'))
  OR (OLD.status='failed' AND NEW.status<>OLD.status)
  OR (OLD.status='succeeded' AND NEW.status<>OLD.status AND NOT (NEW.status='reconciliation_required'
      AND EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=OLD.id AND entry_type='payout_return')))
  OR (OLD.status='reconciliation_required' AND NEW.status='processing')
 ) THEN RAISE EXCEPTION 'seller payout request conflicts with transfer evidence' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END; $$;
CREATE OR REPLACE FUNCTION protect_seller_payout_funding_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP INDEX seller_funding_dispatch_payment_idx;
ALTER TABLE seller_payout_funding_dispatches ADD CONSTRAINT seller_payout_funding_dispatches_payment_id_key UNIQUE(payment_id);
DROP TABLE seller_source_reversal_closures;
DROP FUNCTION protect_seller_source_reversal_closure();
DROP FUNCTION check_seller_source_reversal_closure_consumed();
DROP FUNCTION seller_source_reversal_close_resolution(uuid);
DROP FUNCTION seller_source_reversal_return_proven(uuid);
DROP FUNCTION seller_settlement_has_open_source(uuid);
DROP FUNCTION seller_source_transfer_closed(uuid);
DROP FUNCTION assert_seller_source_reversal_bindings(seller_source_reversal_commands);
