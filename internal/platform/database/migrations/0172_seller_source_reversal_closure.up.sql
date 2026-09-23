-- Drain all financial writers. A returned source is consumed exactly once;
-- original transfer, bank and settlement evidence are never deleted/recredited.
CREATE TABLE seller_source_reversal_closures (
 id uuid PRIMARY KEY,
 command_id uuid NOT NULL UNIQUE REFERENCES seller_source_reversal_commands(id),
 read_id uuid NOT NULL REFERENCES seller_source_reversal_reads(id),
 payout_request_id uuid NOT NULL UNIQUE REFERENCES seller_payout_requests(id),
 settlement_id uuid NOT NULL REFERENCES product_settlements(id),
 seller_id uuid NOT NULL REFERENCES users(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 expected_updated_at timestamptz NOT NULL CHECK(isfinite(expected_updated_at)),
 resolution text NOT NULL CHECK(resolution IN ('released','refund_recovered')),
 recovery_id uuid REFERENCES seller_recovery_obligations(id),
 release_entry_id uuid NOT NULL UNIQUE REFERENCES seller_ledger_entries(id) DEFERRABLE INITIALLY DEFERRED,
 amount_cents integer NOT NULL CHECK(amount_cents>0),
 currency text NOT NULL CHECK(currency='USD'),
 reason text NOT NULL CHECK(char_length(btrim(reason)) BETWEEN 10 AND 1000),
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 request_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 UNIQUE(actor_id,idempotency_key), CHECK(actor_id<>seller_id),
 CHECK((resolution='refund_recovered')=(recovery_id IS NOT NULL))
);

CREATE FUNCTION seller_source_transfer_closed(source uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM seller_source_reversal_closures x
 JOIN seller_source_reversal_commands c ON c.id=x.command_id WHERE c.source_transfer_id=source);
$$;
CREATE FUNCTION seller_settlement_has_open_source(settlement uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM seller_payout_transfers t WHERE t.settlement_id=settlement AND NOT seller_source_transfer_closed(t.id));
$$;

-- Independent of actor and parent status, so deferred consumption checks can
-- verify the same external proof after the request is atomically cancelled.
CREATE FUNCTION seller_source_reversal_return_proven(command uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM seller_source_reversal_commands c
 JOIN seller_source_reversal_results result ON result.command_id=c.id
 JOIN seller_source_reversal_reads accepted ON accepted.id=result.read_id
 CROSS JOIN LATERAL seller_source_reversal_bank_state(c.payout_request_id) bank
 JOIN LATERAL (SELECT * FROM seller_source_reversal_reads r WHERE r.command_id=c.id
  AND r.outcome IS DISTINCT FROM 'superseded' ORDER BY r.started_at DESC,r.id DESC LIMIT 1) latest ON true
 WHERE c.id=command AND latest.finished_at IS NOT NULL AND latest.outcome='found'
 AND NOT latest.requires_review AND latest.error_code='' AND latest.finished_at<=latest.deadline_at
 AND latest.evidence->'transfer'=accepted.evidence->'transfer'
 AND latest.evidence->'observations'=accepted.evidence->'observations'
 AND bank.command_id IS NOT DISTINCT FROM c.bank_command_id
 AND bank.result_id IS NOT DISTINCT FROM c.bank_result_id AND bank.disposition=c.bank_disposition
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_reads r WHERE r.command_id=c.id
  AND (r.finished_at IS NULL OR r.requires_review OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.evidence->'observations') o
   WHERE o->>'providerId' IS DISTINCT FROM result.provider_reversal_id))));
$$;

CREATE FUNCTION seller_source_reversal_close_resolution(command uuid) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN p.status='paid' AND o.status='fulfilled' AND s.status='available'
  AND NOT EXISTS(SELECT 1 FROM seller_recovery_obligations d WHERE d.settlement_id=s.id AND (d.remaining_cents>0 OR d.status IN ('open','reconciliation_required')))
  THEN 'released'
 WHEN p.status='refunded' AND o.status='refunded' AND s.status='recovery_required' AND s.hold_reason='buyer_refund'
  AND s.recovery_amount_cents=c.amount_cents
  AND EXISTS(SELECT 1 FROM seller_recovery_obligations d WHERE d.settlement_id=s.id AND d.seller_id=c.seller_id
   AND d.currency=c.currency AND d.amount_cents=c.amount_cents AND d.remaining_cents=c.amount_cents AND d.status IN ('open','reconciliation_required'))
  AND (SELECT count(*) FROM product_refund_attempts a WHERE a.payment_id=p.id AND a.status='succeeded')=1
  AND EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=p.id AND a.status='succeeded'
   AND a.provider=p.provider AND a.provider_payment_id=p.provider_payment_id AND a.amount_cents=p.amount_cents AND a.currency=p.currency AND NOT a.reconciliation_required)
  THEN 'refund_recovered' END
 FROM seller_source_reversal_commands c JOIN product_settlements s ON s.id=c.settlement_id
 JOIN payment_intents p ON p.id=c.payment_id JOIN orders o ON o.id=s.order_id
 WHERE c.id=command
 AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=p.id AND (a.status IN ('requested','pending') OR a.reconciliation_required));
$$;

CREATE FUNCTION protect_seller_source_reversal_closure() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE command seller_source_reversal_commands;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source return consumption is immutable' USING ERRCODE='55000'; END IF;
 IF current_setting('app.seller_reversal_closure_protocol',true) IS DISTINCT FROM 'closure-v1' THEN
  RAISE EXCEPTION 'source return closure protocol required' USING ERRCODE='23514'; END IF;
 SELECT * INTO STRICT command FROM seller_source_reversal_commands WHERE id=NEW.command_id;
 PERFORM assert_seller_source_reversal_bindings(command);
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=NEW.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR NEW.actor_id=command.seller_id
 OR ROW(NEW.payout_request_id,NEW.settlement_id,NEW.seller_id,NEW.amount_cents,NEW.currency)
  IS DISTINCT FROM ROW(command.payout_request_id,command.settlement_id,command.seller_id,command.amount_cents,command.currency)
 OR NOT EXISTS(SELECT 1 FROM seller_payout_requests WHERE id=NEW.payout_request_id AND updated_at=NEW.expected_updated_at AND status='reconciliation_required')
 OR NOT EXISTS(SELECT 1 FROM seller_source_reversal_results WHERE command_id=NEW.command_id AND read_id=NEW.read_id)
 OR NOT seller_source_reversal_return_proven(NEW.command_id)
 OR seller_source_reversal_close_resolution(NEW.command_id) IS DISTINCT FROM NEW.resolution
 OR (NEW.recovery_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM seller_recovery_obligations d
  WHERE d.id=NEW.recovery_id AND d.settlement_id=NEW.settlement_id AND d.seller_id=NEW.seller_id))
 OR NEW.created_at>clock_timestamp() OR NEW.created_at<clock_timestamp()-interval '1 minute' THEN
  RAISE EXCEPTION 'source return closure requires current confirmed funds and bank proof' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_reversal_closure_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_source_reversal_closures
 FOR EACH ROW EXECUTE FUNCTION protect_seller_source_reversal_closure();

CREATE UNIQUE INDEX seller_ledger_release_once ON seller_ledger_entries(payout_request_id) WHERE entry_type='payout_release';
CREATE FUNCTION protect_seller_source_release_ledger() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.entry_type<>'payout_release' OR NOT EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=NEW.payout_request_id) THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM seller_source_reversal_closures x JOIN seller_payout_requests r ON r.id=x.payout_request_id
  WHERE x.release_entry_id=NEW.id AND x.payout_request_id=NEW.payout_request_id AND x.seller_id=NEW.seller_id
  AND x.amount_cents=NEW.amount_cents AND x.currency=NEW.currency AND r.status='cancelled'
  AND NEW.idempotency_key='payout-release:'||r.id::text AND NEW.evidence=jsonb_build_object('sourceReversalClosureId',x.id)) THEN
  RAISE EXCEPTION 'source release requires consumed reversal proof' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_release_ledger_guard BEFORE INSERT ON seller_ledger_entries
 FOR EACH ROW EXECUTE FUNCTION protect_seller_source_release_ledger();

CREATE FUNCTION check_seller_source_reversal_closure_consumed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT seller_source_reversal_return_proven(NEW.command_id)
 OR NOT EXISTS(SELECT 1 FROM seller_payout_requests WHERE id=NEW.payout_request_id AND status='cancelled')
 OR NOT EXISTS(SELECT 1 FROM seller_payout_request_allocations WHERE payout_request_id=NEW.payout_request_id
  AND settlement_id=NEW.settlement_id AND released_at IS NOT NULL)
 OR NOT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE id=NEW.release_entry_id AND entry_type='payout_release'
  AND payout_request_id=NEW.payout_request_id AND amount_cents=NEW.amount_cents AND seller_id=NEW.seller_id)
 OR NOT EXISTS(SELECT 1 FROM seller_payout_request_events WHERE payout_request_id=NEW.payout_request_id
  AND event_key='source-reversal-closed:'||NEW.id::text AND event_type='source_reversal.closed'
  AND to_status='cancelled' AND evidence->>'closureId'=NEW.id::text)
 OR NOT EXISTS(SELECT 1 FROM audit_events WHERE action='seller_payout.source_reversal_closed'
  AND resource_id=NEW.payout_request_id AND actor_id=NEW.actor_id AND metadata->>'closureId'=NEW.id::text)
 OR (NEW.resolution='released' AND seller_source_reversal_close_resolution(NEW.command_id) IS DISTINCT FROM 'released')
 OR (NEW.resolution='refund_recovered' AND NOT EXISTS(SELECT 1 FROM seller_recovery_obligations d
  JOIN product_settlements s ON s.id=d.settlement_id JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id
  WHERE d.id=NEW.recovery_id AND d.remaining_cents=0 AND d.status='settled' AND s.status='cancelled'
  AND s.recovery_amount_cents=0 AND p.status='refunded' AND o.status='refunded')) THEN
  RAISE EXCEPTION 'source return must atomically consume ledger allocation debt event and audit' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER seller_source_reversal_closure_consumed AFTER INSERT ON seller_source_reversal_closures
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_seller_source_reversal_closure_consumed();

CREATE FUNCTION protect_consumed_source_recovery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE recovery_id=OLD.id) THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'consumed recovery evidence is immutable' USING ERRCODE='55000'; END IF;
  IF ROW(NEW.id,NEW.seller_id,NEW.settlement_id,NEW.amount_cents,NEW.currency,NEW.created_at)
    IS DISTINCT FROM ROW(OLD.id,OLD.seller_id,OLD.settlement_id,OLD.amount_cents,OLD.currency,OLD.created_at)
   OR NEW.remaining_cents<>0 OR NEW.status<>'settled' THEN
   RAISE EXCEPTION 'consumed recovery cannot reopen debt' USING ERRCODE='55000'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER consumed_source_recovery_guard BEFORE UPDATE OR DELETE ON seller_recovery_obligations
 FOR EACH ROW EXECUTE FUNCTION protect_consumed_source_recovery();

CREATE FUNCTION protect_closed_source_settlement_writer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.status,NEW.recovery_amount_cents) IS DISTINCT FROM ROW(OLD.status,OLD.recovery_amount_cents)
 AND EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE settlement_id=OLD.id)
 AND current_setting('app.seller_reversal_closure_protocol',true) IS DISTINCT FROM 'closure-v1' THEN
  RAISE EXCEPTION 'closed source requires proof-aware settlement writer' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER closed_source_settlement_writer BEFORE UPDATE ON product_settlements
 FOR EACH ROW EXECUTE FUNCTION protect_closed_source_settlement_writer();


CREATE FUNCTION assert_seller_source_reversal_bindings(command seller_source_reversal_commands) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM lock_seller_payout_disposition(command.payout_request_id);
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

CREATE OR REPLACE FUNCTION assert_seller_source_reversal_command(command seller_source_reversal_commands) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM assert_seller_source_reversal_bindings(command);
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=command.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR command.actor_id=command.seller_id THEN
  RAISE EXCEPTION 'reversal requires current independent finance authority' USING ERRCODE='23514'; END IF;
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
       OR (EXISTS(SELECT 1 FROM seller_payout_transfers WHERE payout_request_id=request.id)
           AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE payout_request_id=request.id))
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
     OR seller_settlement_has_open_source(settlement.id)
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
 IF NEW.status='cancelled' AND OLD.status='reconciliation_required'
 AND EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE payout_request_id=OLD.id) THEN RETURN NEW; END IF;
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
 IF NEW.status='cancelled' AND OLD.status='reconciliation_required'
 AND EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE payout_request_id=OLD.id) THEN RETURN NEW; END IF;
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

-- A fully returned and consumed source can be funded again without reusing
-- the old provider idempotency key. Financial locks and the insert guard
-- retain one unresolved source per payment; historical dispatches survive.
ALTER TABLE seller_payout_funding_dispatches DROP CONSTRAINT seller_payout_funding_dispatches_payment_id_key;
CREATE INDEX seller_funding_dispatch_payment_idx ON seller_payout_funding_dispatches(payment_id);
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
      AND c.identity=t.provider_identity AND (
        t.dispatch_key='transfer-'||p.id::text OR
        (current_setting('app.seller_reversal_closure_protocol',true)='closure-v1'
         AND t.dispatch_key='seller-source-'||r.id::text
         AND EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE settlement_id=s.id)))
      AND NOT EXISTS (SELECT 1 FROM seller_payout_funding_dispatches previous
        WHERE previous.payment_id=p.id AND NOT seller_source_transfer_closed(previous.transfer_id))
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
