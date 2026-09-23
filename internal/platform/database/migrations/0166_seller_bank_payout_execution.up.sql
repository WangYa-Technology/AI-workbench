-- Bank execution is distinct from platform-to-Connect source funding.
CREATE TABLE seller_bank_payout_dispatches (
 command_id uuid PRIMARY KEY REFERENCES seller_bank_payout_commands(id),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 started_at timestamptz,
 deadline_at timestamptz,
 CHECK ((started_at IS NULL AND deadline_at IS NULL) OR
        (started_at>=created_at AND deadline_at=started_at+interval '20 seconds'))
);
CREATE FUNCTION protect_seller_bank_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE command seller_bank_payout_commands;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'bank dispatch is immutable' USING ERRCODE='55000'; END IF;
 IF current_setting('app.seller_bank_execution_protocol',true) IS DISTINCT FROM 'journal-v1' THEN
  RAISE EXCEPTION 'bank execution protocol required' USING ERRCODE='23514';
 END IF;
 SELECT * INTO STRICT command FROM seller_bank_payout_commands WHERE id=NEW.command_id;
 IF TG_OP='INSERT' THEN
  IF NEW.started_at IS NOT NULL OR NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id
   AND kind='payment.execute_seller_bank_payout' AND payload=jsonb_build_object('commandId',NEW.command_id) AND status='queued') THEN
   RAISE EXCEPTION 'bank dispatch requires its original queued job' USING ERRCODE='23514';
  END IF;
  PERFORM assert_seller_bank_payout_command(command);
 ELSE
  IF ROW(NEW.command_id,NEW.job_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.command_id,OLD.job_id,OLD.created_at)
   OR OLD.started_at IS NOT NULL OR NEW.started_at IS NULL
   OR NEW.started_at<clock_timestamp()-interval '1 minute' OR NEW.started_at>clock_timestamp() THEN
   RAISE EXCEPTION 'bank dispatch can only start once' USING ERRCODE='55000';
  END IF;
  PERFORM assert_seller_bank_payout_command(command);
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_bank_dispatch_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_bank_payout_dispatches
 FOR EACH ROW EXECUTE FUNCTION protect_seller_bank_dispatch();

CREATE TABLE seller_bank_payout_reads (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 command_id uuid NOT NULL REFERENCES seller_bank_payout_dispatches(command_id),
 kind text NOT NULL CHECK(kind IN ('create','query')),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 deadline_at timestamptz NOT NULL,
 finished_at timestamptz,
 outcome text,
 requires_review boolean NOT NULL DEFAULT false,
 error_code text,
 evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
 CHECK (deadline_at>started_at AND deadline_at<=started_at+interval '20 seconds'),
 CHECK (((finished_at IS NULL AND outcome IS NULL AND NOT requires_review AND error_code IS NULL AND evidence='{}'::jsonb)
 OR (finished_at>=started_at AND outcome IN ('found','not_found','incomplete','ambiguous','error','superseded')
 AND jsonb_typeof(evidence)='object' AND jsonb_typeof(evidence->'observations')='array'
 AND jsonb_array_length(evidence->'observations')<=2)) IS TRUE)
);
CREATE UNIQUE INDEX seller_bank_create_once ON seller_bank_payout_reads(command_id) WHERE kind='create';
CREATE INDEX seller_bank_reads_command_idx ON seller_bank_payout_reads(command_id,started_at,id);
CREATE FUNCTION protect_seller_bank_read() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'bank observations are immutable' USING ERRCODE='55000'; END IF;
 IF current_setting('app.seller_bank_execution_protocol',true) IS DISTINCT FROM 'journal-v1' THEN
  RAISE EXCEPTION 'bank execution protocol required' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.finished_at IS NOT NULL OR NEW.started_at>clock_timestamp() OR NEW.started_at<clock_timestamp()-interval '1 minute'
   OR NOT EXISTS(SELECT 1 FROM seller_bank_payout_dispatches WHERE command_id=NEW.command_id AND started_at IS NOT NULL)
   OR (NEW.kind='create' AND NOT EXISTS(SELECT 1 FROM seller_bank_payout_dispatches
       WHERE command_id=NEW.command_id AND deadline_at=NEW.deadline_at)) THEN
   RAISE EXCEPTION 'bank read must be registered before execution' USING ERRCODE='23514';
  END IF;
 ELSIF ROW(NEW.id,NEW.command_id,NEW.kind,NEW.started_at,NEW.deadline_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.command_id,OLD.kind,OLD.started_at,OLD.deadline_at)
  OR OLD.finished_at IS NOT NULL OR NEW.finished_at IS NULL THEN
  RAISE EXCEPTION 'bank observations are immutable' USING ERRCODE='55000';
 END IF;
 IF NEW.finished_at IS NOT NULL AND NEW.finished_at>NEW.deadline_at AND NEW.outcome='found' THEN
  RAISE EXCEPTION 'late bank result requires another authenticated read' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_bank_read_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_bank_payout_reads
 FOR EACH ROW EXECUTE FUNCTION protect_seller_bank_read();

-- One immutable row per accepted provider status. Ambiguous observations never
-- enter this projection; raw reads, including partial failures, remain above.
CREATE TABLE seller_bank_payout_results (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 command_id uuid NOT NULL REFERENCES seller_bank_payout_commands(id),
 read_id uuid NOT NULL UNIQUE REFERENCES seller_bank_payout_reads(id),
 provider_payout_id text NOT NULL CHECK(provider_payout_id ~ '^po_[A-Za-z0-9_]{6,252}$'),
 status text NOT NULL CHECK(status IN ('pending','in_transit','paid','failed','canceled')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(command_id,status)
);
CREATE FUNCTION protect_seller_bank_result() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE command seller_bank_payout_commands; observation jsonb;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'bank results are immutable' USING ERRCODE='55000'; END IF;
 SELECT * INTO STRICT command FROM seller_bank_payout_commands WHERE id=NEW.command_id;
 SELECT evidence->'observations'->0 INTO observation FROM seller_bank_payout_reads
 WHERE id=NEW.read_id AND command_id=NEW.command_id AND finished_at IS NOT NULL
 AND NOT requires_review AND outcome='found' AND error_code='' AND jsonb_array_length(evidence->'observations')=1;
 IF observation IS NULL OR NOT (
   observation->>'providerId'=NEW.provider_payout_id AND observation->>'status'=NEW.status
   AND observation->>'destination'=command.destination_id AND observation->>'bankDestinationId'=command.bank_destination_id
   AND (observation->>'amountCents')::integer=command.amount_cents AND observation->>'currency'=command.currency
   AND (observation->>'createdAt')::timestamptz BETWEEN command.created_at-interval '5 minutes' AND command.created_at+interval '23 hours 5 minutes'
   AND (observation->>'createdAt')::timestamptz<=clock_timestamp()+interval '1 minute'
 ) IS TRUE OR EXISTS(SELECT 1 FROM seller_bank_payout_reads r WHERE r.command_id=NEW.command_id
 AND r.finished_at IS NOT NULL AND (r.requires_review OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.evidence->'observations') o
 WHERE o->>'providerId' IS DISTINCT FROM NEW.provider_payout_id)))
 OR EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=NEW.command_id AND provider_payout_id<>NEW.provider_payout_id)
 OR (NEW.status='paid' AND EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=NEW.command_id AND status IN ('failed','canceled'))) THEN
  RAISE EXCEPTION 'bank result does not match a complete unambiguous observation' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_bank_result_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_bank_payout_results
 FOR EACH ROW EXECUTE FUNCTION protect_seller_bank_result();

ALTER TABLE seller_ledger_entries DROP CONSTRAINT seller_ledger_entries_entry_type_check;
ALTER TABLE seller_ledger_entries DROP CONSTRAINT seller_ledger_entries_check1;
ALTER TABLE seller_ledger_entries ADD CONSTRAINT seller_ledger_entries_entry_type_check
 CHECK(entry_type IN ('settlement_credit','recovery_debit','payout_reservation','payout_release','adjustment','payout_debit','payout_return'));
ALTER TABLE seller_ledger_entries ADD CONSTRAINT seller_ledger_entries_check1
 CHECK((entry_type IN ('payout_reservation','payout_release','payout_debit','payout_return'))=(payout_request_id IS NOT NULL));
CREATE UNIQUE INDEX seller_ledger_bank_once ON seller_ledger_entries(payout_request_id,entry_type)
 WHERE entry_type IN ('payout_debit','payout_return');
CREATE FUNCTION protect_seller_bank_ledger() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.entry_type NOT IN ('payout_debit','payout_return') THEN RETURN NEW; END IF;
 IF current_setting('app.seller_bank_execution_protocol',true) IS DISTINCT FROM 'journal-v1'
 OR NOT EXISTS(SELECT 1 FROM seller_bank_payout_commands c JOIN seller_bank_payout_results r ON r.command_id=c.id
  WHERE c.payout_request_id=NEW.payout_request_id AND c.seller_id=NEW.seller_id
  AND c.amount_cents=NEW.amount_cents AND c.currency=NEW.currency
  AND NEW.idempotency_key=NEW.entry_type||':'||c.id::text AND NEW.evidence=jsonb_build_object('bankResultId',r.id)
  AND ((NEW.entry_type='payout_debit' AND r.status='paid') OR (NEW.entry_type='payout_return' AND r.status IN ('failed','canceled')
       AND EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=c.payout_request_id AND entry_type='payout_debit')))) THEN
  RAISE EXCEPTION 'bank ledger requires matching immutable bank result' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_bank_ledger_guard BEFORE INSERT ON seller_ledger_entries
 FOR EACH ROW EXECUTE FUNCTION protect_seller_bank_ledger();

CREATE OR REPLACE VIEW seller_funds_ledger_scopes AS
SELECT e.id AS ledger_id,e.seller_id,e.entry_type,e.amount_cents,e.currency,e.settlement_id,e.payout_request_id,e.available_at,
 CASE WHEN e.entry_type='settlement_credit' AND e.seller_id=s.seller_id AND e.currency=s.currency AND e.amount_cents=ps.net_amount_cents THEN s.account_id
 WHEN e.entry_type IN ('payout_reservation','payout_release','payout_debit','payout_return')
 AND e.seller_id=r.seller_id AND e.currency=r.currency AND e.amount_cents=r.amount_cents THEN r.account_id
 ELSE NULL END AS account_id
FROM seller_ledger_entries e LEFT JOIN product_settlements ps ON ps.id=e.settlement_id
LEFT JOIN seller_funds_settlement_scopes s ON s.settlement_id=e.settlement_id
LEFT JOIN seller_funds_request_scopes r ON r.payout_request_id=e.payout_request_id;

CREATE OR REPLACE FUNCTION reject_unconfirmed_seller_bank_payout() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='succeeded' AND (NOT EXISTS(SELECT 1 FROM seller_ledger_entries
  WHERE payout_request_id=NEW.id AND entry_type='payout_debit' AND amount_cents=NEW.amount_cents AND seller_id=NEW.seller_id)
 OR EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=NEW.id AND entry_type='payout_return')) THEN
  RAISE EXCEPTION 'bank payout success requires consumed bank evidence' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END; $$;
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

-- A result and its money projection must commit together. This also prevents
-- accepting a paid/returned bank result while forgetting its ledger effect.
CREATE FUNCTION check_seller_bank_result_consumed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE request uuid; debit boolean; returned boolean; parent text;
BEGIN
 SELECT payout_request_id INTO STRICT request FROM seller_bank_payout_commands WHERE id=NEW.command_id;
 SELECT EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=request AND entry_type='payout_debit'),
 EXISTS(SELECT 1 FROM seller_ledger_entries WHERE payout_request_id=request AND entry_type='payout_return') INTO debit,returned;
 SELECT status INTO STRICT parent FROM seller_payout_requests WHERE id=request;
 IF (NEW.status='paid' AND (NOT debit OR (NOT returned AND parent<>'succeeded')))
 OR (NEW.status IN ('failed','canceled') AND (debit AND NOT returned OR parent<>'reconciliation_required')) THEN
  RAISE EXCEPTION 'bank result requires atomic ledger and parent projection' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER seller_bank_result_consumed AFTER INSERT ON seller_bank_payout_results
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_seller_bank_result_consumed();

CREATE TABLE seller_bank_payout_checks (
 job_id uuid PRIMARY KEY REFERENCES jobs(id),
 command_id uuid NOT NULL REFERENCES seller_bank_payout_dispatches(command_id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX seller_bank_checks_history_idx ON seller_bank_payout_checks(command_id,created_at DESC,job_id DESC);
CREATE FUNCTION protect_seller_bank_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'bank check binding is immutable' USING ERRCODE='55000'; END IF;
 IF NOT EXISTS(SELECT 1 FROM seller_bank_payout_dispatches d JOIN jobs j ON j.id=NEW.job_id
  WHERE d.command_id=NEW.command_id AND d.started_at IS NOT NULL
  AND j.kind='payment.check_seller_bank_payout' AND j.payload=jsonb_build_object('commandId',NEW.command_id) AND j.status='queued') THEN
  RAISE EXCEPTION 'bank check requires original dispatch evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_bank_check_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_bank_payout_checks
 FOR EACH ROW EXECUTE FUNCTION protect_seller_bank_check();

-- Paid payouts remain observable: a bank can return money after initial paid.
-- Failed/canceled results retain their reservation for explicit reconciliation.
CREATE VIEW seller_bank_payout_check_candidates AS
 SELECT d.command_id,c.payment_id,
 GREATEST(d.started_at,j.updated_at,last_check.created_at,last_job.updated_at,last_read.finished_at)
  + CASE WHEN EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=c.id AND status='paid')
    THEN interval '24 hours' ELSE interval '5 minutes' END AS due_at
 FROM seller_bank_payout_dispatches d JOIN seller_bank_payout_commands c ON c.id=d.command_id
 JOIN jobs j ON j.id=d.job_id
 LEFT JOIN LATERAL (SELECT x.* FROM seller_bank_payout_checks x WHERE x.command_id=d.command_id
 ORDER BY x.created_at DESC,x.job_id DESC LIMIT 1) last_check ON true
 LEFT JOIN jobs last_job ON last_job.id=last_check.job_id
 LEFT JOIN LATERAL (SELECT max(r.finished_at) finished_at FROM seller_bank_payout_reads r WHERE r.command_id=d.command_id) last_read ON true
 WHERE d.started_at IS NOT NULL AND j.status IN ('failed','cancelled','succeeded')
 AND NOT EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=c.id AND status IN ('failed','canceled'))
 AND NOT EXISTS(SELECT 1 FROM seller_bank_payout_checks x JOIN jobs active ON active.id=x.job_id
 WHERE x.command_id=d.command_id AND active.status IN ('queued','running'));

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation','product_settlement_reconciliation',
 'seller_funding_reconciliation','seller_bank_reconciliation'));
