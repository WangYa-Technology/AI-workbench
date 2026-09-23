-- Drain financial writers before upgrading. A source reversal is a separate
-- finance decision, not a cancellation or permission to release local funds.
LOCK TABLE seller_payout_requests IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE seller_payout_disposition_locks (
 payout_request_id uuid PRIMARY KEY REFERENCES seller_payout_requests(id),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0)
);
INSERT INTO seller_payout_disposition_locks(payout_request_id) SELECT id FROM seller_payout_requests;
CREATE FUNCTION create_seller_payout_disposition_lock() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO seller_payout_disposition_locks(payout_request_id) VALUES(NEW.id);
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_payout_disposition_insert AFTER INSERT ON seller_payout_requests
 FOR EACH ROW EXECUTE FUNCTION create_seller_payout_disposition_lock();

CREATE FUNCTION lock_seller_payout_disposition(request uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE owner_id uuid;
BEGIN
 IF current_setting('app.seller_reversal_protocol',true) IS DISTINCT FROM 'command-v1' THEN
  RAISE EXCEPTION 'source reversal disposition protocol required' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM seller_payout_request_allocations a JOIN product_settlements s ON s.id=a.settlement_id
 JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id
 WHERE a.payout_request_id=request FOR UPDATE OF p,o;
 SELECT seller_id INTO STRICT owner_id FROM seller_payout_requests WHERE id=request;
 PERFORM pg_advisory_xact_lock(hashtextextended(owner_id::text,0));
 PERFORM 1 FROM seller_payout_requests WHERE id=request FOR UPDATE;
 PERFORM 1 FROM product_settlements s JOIN seller_payout_request_allocations a ON a.settlement_id=s.id
 WHERE a.payout_request_id=request FOR UPDATE OF s;
 -- Write the tuple, not just lock it: a waiting old RR/Serializable snapshot
 -- must abort instead of overlooking a newly committed bank/reversal record.
 UPDATE seller_payout_disposition_locks SET revision=revision+1 WHERE payout_request_id=request;
 IF NOT FOUND THEN RAISE EXCEPTION 'missing payout disposition control' USING ERRCODE='23514'; END IF;
END; $$;

CREATE TABLE seller_source_reversal_commands (
 id uuid PRIMARY KEY,
 payout_request_id uuid NOT NULL UNIQUE REFERENCES seller_payout_requests(id),
 source_transfer_id uuid NOT NULL UNIQUE REFERENCES seller_payout_transfers(id),
 settlement_id uuid NOT NULL REFERENCES product_settlements(id),
 payment_id uuid NOT NULL REFERENCES payment_intents(id),
 seller_id uuid NOT NULL REFERENCES users(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 request_updated_at timestamptz NOT NULL CHECK(isfinite(request_updated_at)),
 bank_command_id uuid REFERENCES seller_bank_payout_commands(id),
 bank_result_id uuid REFERENCES seller_bank_payout_results(id),
 bank_disposition text NOT NULL CHECK(bank_disposition IN ('not_reserved','not_started','failed','returned')),
 provider_identity jsonb NOT NULL CHECK(jsonb_typeof(provider_identity)='object'),
 provider_transfer_id text NOT NULL CHECK(provider_transfer_id ~ '^tr_[A-Za-z0-9_]{6,252}$'),
 provider_charge_id text NOT NULL CHECK(provider_charge_id ~ '^ch_[A-Za-z0-9_]{6,252}$'),
 destination_id text NOT NULL CHECK(destination_id ~ '^acct_[A-Za-z0-9_]{6,250}$'),
 amount_cents integer NOT NULL CHECK(amount_cents BETWEEN 1 AND 99999999),
 currency text NOT NULL CHECK(currency='USD'),
 live_mode boolean NOT NULL,
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 dispatch_key text NOT NULL UNIQUE CHECK(dispatch_key='seller-source-reversal-'||id::text),
 reason text NOT NULL CHECK(char_length(btrim(reason)) BETWEEN 10 AND 1000),
 request_id text NOT NULL,
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 UNIQUE(actor_id,idempotency_key), CHECK(actor_id<>seller_id),
 CHECK((bank_disposition='not_reserved')=(bank_command_id IS NULL)),
 CHECK((bank_disposition IN ('failed','returned'))=(bank_result_id IS NOT NULL))
);
CREATE INDEX seller_source_reversal_owner_idx ON seller_source_reversal_commands(seller_id,created_at,id);

-- Returns evidence, never guesses from queue status. Used after the common
-- financial locks. The caller must match the exact observed IDs and version.
CREATE FUNCTION seller_source_reversal_bank_state(request uuid)
 RETURNS TABLE(command_id uuid,result_id uuid,disposition text) LANGUAGE plpgsql AS $$
DECLARE bank seller_bank_payout_commands; started timestamptz; terminal seller_bank_payout_results;
 debit boolean; returned boolean; last_read seller_bank_payout_reads;
BEGIN
 SELECT * INTO bank FROM seller_bank_payout_commands WHERE payout_request_id=request;
 IF NOT FOUND THEN
  IF EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=request AND entry_type IN ('payout_debit','payout_return')) THEN RETURN; END IF;
  RETURN QUERY SELECT NULL::uuid,NULL::uuid,'not_reserved'::text;
  RETURN;
 END IF;
 SELECT started_at INTO started FROM seller_bank_payout_dispatches WHERE seller_bank_payout_dispatches.command_id=bank.id;
 SELECT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=request AND entry_type='payout_debit'),
 EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=request AND entry_type='payout_return') INTO debit,returned;
 IF started IS NULL THEN
  IF debit OR returned OR EXISTS(SELECT 1 FROM seller_bank_payout_reads r WHERE r.command_id=bank.id)
   OR EXISTS(SELECT 1 FROM seller_bank_payout_results r WHERE r.command_id=bank.id) THEN RETURN; END IF;
  RETURN QUERY SELECT bank.id,NULL::uuid,'not_started'::text;
  RETURN;
 END IF;
 -- Open observations and any contradictory evidence require reconciliation.
 IF EXISTS(SELECT 1 FROM seller_bank_payout_reads r WHERE r.command_id=bank.id AND (r.finished_at IS NULL OR r.requires_review)) THEN RETURN; END IF;
 SELECT * INTO terminal FROM seller_bank_payout_results r WHERE r.command_id=bank.id AND r.status IN ('failed','canceled')
 ORDER BY r.created_at DESC,r.id DESC LIMIT 1;
 IF NOT FOUND OR debit<>returned OR EXISTS(SELECT 1 FROM seller_bank_payout_results r
 WHERE r.command_id=bank.id AND r.provider_payout_id<>terminal.provider_payout_id) THEN RETURN; END IF;
 SELECT * INTO last_read FROM seller_bank_payout_reads r WHERE r.command_id=bank.id
 ORDER BY r.started_at DESC,r.id DESC LIMIT 1;
 IF NOT FOUND OR last_read.outcome<>'found' OR last_read.error_code IS DISTINCT FROM ''
 OR jsonb_array_length(last_read.evidence->'observations')<>1
 OR (last_read.evidence->'observations'->0->>'providerId') IS DISTINCT FROM terminal.provider_payout_id
 OR (last_read.evidence->'observations'->0->>'status') IS DISTINCT FROM terminal.status THEN RETURN; END IF;
 RETURN QUERY SELECT bank.id,terminal.id,CASE WHEN debit THEN 'returned' ELSE 'failed' END;
END; $$;

CREATE FUNCTION assert_seller_source_reversal_command(command seller_source_reversal_commands) RETURNS void LANGUAGE plpgsql AS $$
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

CREATE FUNCTION protect_seller_source_reversal_command() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source reversal command is immutable' USING ERRCODE='55000'; END IF;
 PERFORM assert_seller_source_reversal_command(NEW);
 IF NEW.created_at>clock_timestamp() OR NEW.created_at<clock_timestamp()-interval '1 minute'
 OR NOT EXISTS(SELECT 1 FROM seller_payout_requests WHERE id=NEW.payout_request_id AND updated_at=NEW.request_updated_at)
 OR NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id AND kind='payment.reverse_seller_source'
  AND payload=jsonb_build_object('commandId',NEW.id) AND status='queued') THEN
  RAISE EXCEPTION 'reversal requires current request and original queued job' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_reversal_command_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_source_reversal_commands
 FOR EACH ROW EXECUTE FUNCTION protect_seller_source_reversal_command();

-- Reads remain possible after a reversal decision, but every new or finished
-- bank observation changes the disposition tuple so old snapshots cannot
-- authorize a reversal while overlooking a newly unknown/contradictory read.
CREATE FUNCTION serialize_seller_bank_observation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE request uuid;
BEGIN
 SELECT payout_request_id INTO STRICT request FROM seller_bank_payout_commands WHERE id=NEW.command_id;
 PERFORM lock_seller_payout_disposition(request);
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_bank_read_disposition BEFORE INSERT OR UPDATE ON seller_bank_payout_reads
 FOR EACH ROW EXECUTE FUNCTION serialize_seller_bank_observation();
CREATE TRIGGER seller_bank_result_disposition BEFORE INSERT ON seller_bank_payout_results
 FOR EACH ROW EXECUTE FUNCTION serialize_seller_bank_observation();

CREATE OR REPLACE FUNCTION assert_seller_bank_payout_command(command seller_bank_payout_commands) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM lock_seller_payout_disposition(command.payout_request_id);
 IF EXISTS(SELECT 1 FROM seller_source_reversal_commands WHERE payout_request_id=command.payout_request_id) THEN
  RAISE EXCEPTION 'source reversal command prevents new bank execution' USING ERRCODE='23514';
 END IF;
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


LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
CREATE OR REPLACE FUNCTION protect_payment_job_execution_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  PERFORM 1 FROM payment_job_execution_locks WHERE job_id=OLD.id FOR UPDATE;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.kind,NEW.payload,NEW.status) IS NOT DISTINCT FROM ROW(OLD.kind,OLD.payload,OLD.status) THEN
   RETURN NEW;
  END IF;
  IF OLD.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout','payment.reverse_seller_source')
   OR NEW.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout','payment.reverse_seller_source') THEN
   -- Change the tuple as well as locking it. A stale Repeatable Read or
   -- Serializable sender must fail serialization instead of seeing a job
   -- snapshot from before a committed cancellation or payload change.
   INSERT INTO payment_job_execution_locks(job_id) VALUES(NEW.id)
    ON CONFLICT(job_id) DO UPDATE SET revision=payment_job_execution_locks.revision+1;
  END IF;
 ELSE
  IF NEW.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout','payment.reverse_seller_source') THEN
   INSERT INTO payment_job_execution_locks(job_id) VALUES(NEW.id);
  END IF;
 END IF;
 RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION payment_execution_job_matches(execution_job uuid,expected_kind text,expected_payload jsonb)
RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('app.payment_execution_lock_protocol',true) IS DISTINCT FROM 'lock-v1'
 OR expected_kind NOT IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout','payment.reverse_seller_source') THEN
  RETURN false;
 END IF;
 -- Lock before reading the job. Read Committed observes a preceding stop;
 -- stale Repeatable Read/Serializable snapshots fail on the revised tuple.
 PERFORM 1 FROM payment_job_execution_locks WHERE job_id=execution_job FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM 1 FROM jobs j WHERE j.id=execution_job AND j.kind=expected_kind
 AND j.payload=expected_payload AND j.status IN ('queued','running');
 RETURN FOUND;
END; $$;
