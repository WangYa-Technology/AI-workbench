-- Source-return evidence is not a local release. Keep source and bank history
-- intact; a separate, evidence-backed ledger consumer must close the request.
CREATE TABLE seller_source_reversal_dispatches (
 command_id uuid PRIMARY KEY REFERENCES seller_source_reversal_commands(id),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 deadline_at timestamptz NOT NULL,
 CHECK(isfinite(started_at) AND deadline_at=started_at+interval '20 seconds')
);
CREATE FUNCTION assert_seller_source_reversal_execution(command uuid, execution uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE original seller_source_reversal_commands;
BEGIN
 IF current_setting('app.seller_reversal_execution_protocol',true) IS DISTINCT FROM 'journal-v1' THEN
  RAISE EXCEPTION 'source reversal execution protocol required' USING ERRCODE='23514';
 END IF;
 SELECT * INTO STRICT original FROM seller_source_reversal_commands WHERE id=command;
 PERFORM assert_seller_source_reversal_command(original);
 IF original.job_id<>execution OR clock_timestamp()>=original.created_at+interval '23 hours'
 OR NOT payment_execution_job_matches(execution,'payment.reverse_seller_source',jsonb_build_object('commandId',command)) THEN
  RAISE EXCEPTION 'source reversal execution expired, stopped or invalid' USING ERRCODE='23514';
 END IF;
END; $$;
CREATE FUNCTION protect_seller_source_reversal_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source reversal first send is immutable' USING ERRCODE='55000'; END IF;
 PERFORM assert_seller_source_reversal_execution(NEW.command_id,NEW.job_id);
 IF NEW.started_at>clock_timestamp() OR NEW.started_at<clock_timestamp()-interval '1 minute' THEN
  RAISE EXCEPTION 'source reversal must register current first send' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_reversal_dispatch_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_source_reversal_dispatches
 FOR EACH ROW EXECUTE FUNCTION protect_seller_source_reversal_dispatch();

CREATE TABLE seller_source_reversal_reads (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 command_id uuid NOT NULL REFERENCES seller_source_reversal_dispatches(command_id),
 kind text NOT NULL CHECK(kind IN ('create','query')),
 started_at timestamptz NOT NULL,
 deadline_at timestamptz NOT NULL,
 finished_at timestamptz,
 outcome text,
 requires_review boolean NOT NULL DEFAULT false,
 error_code text,
 evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
 CHECK(isfinite(started_at) AND deadline_at>started_at AND deadline_at<=started_at+interval '20 seconds'),
 CHECK(((finished_at IS NULL AND outcome IS NULL AND NOT requires_review AND error_code IS NULL AND evidence='{}'::jsonb)
 OR (isfinite(finished_at) AND finished_at>=started_at AND outcome IN ('found','not_found','incomplete','ambiguous','error','superseded')
 AND jsonb_typeof(evidence)='object' AND jsonb_typeof(evidence->'observations')='array'
 AND jsonb_array_length(evidence->'observations')<=2)) IS TRUE)
);
CREATE UNIQUE INDEX seller_source_reversal_create_once ON seller_source_reversal_reads(command_id) WHERE kind='create';
CREATE INDEX seller_source_reversal_reads_command_idx ON seller_source_reversal_reads(command_id,started_at,id);
CREATE FUNCTION protect_seller_source_reversal_read() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE request uuid;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'source reversal reads are immutable' USING ERRCODE='55000'; END IF;
 IF current_setting('app.seller_reversal_execution_protocol',true) IS DISTINCT FROM 'journal-v1' THEN
  RAISE EXCEPTION 'source reversal execution protocol required' USING ERRCODE='23514';
 END IF;
 SELECT payout_request_id INTO STRICT request FROM seller_source_reversal_commands WHERE id=NEW.command_id;
 PERFORM lock_seller_payout_disposition(request);
 IF TG_OP='INSERT' THEN
  IF NEW.finished_at IS NOT NULL OR NEW.started_at>clock_timestamp() OR NEW.started_at<clock_timestamp()-interval '1 minute'
  OR NOT EXISTS(SELECT 1 FROM seller_source_reversal_dispatches WHERE command_id=NEW.command_id)
  OR (NEW.kind='create' AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_dispatches
   WHERE command_id=NEW.command_id AND started_at=NEW.started_at AND deadline_at=NEW.deadline_at)) THEN
   RAISE EXCEPTION 'source reversal read must precede execution' USING ERRCODE='23514';
  END IF;
 ELSIF ROW(NEW.id,NEW.command_id,NEW.kind,NEW.started_at,NEW.deadline_at)
   IS DISTINCT FROM ROW(OLD.id,OLD.command_id,OLD.kind,OLD.started_at,OLD.deadline_at)
   OR OLD.finished_at IS NOT NULL OR NEW.finished_at IS NULL THEN
  RAISE EXCEPTION 'source reversal reads are immutable' USING ERRCODE='55000';
 END IF;
 IF NEW.finished_at IS NOT NULL AND NEW.outcome='found' AND NEW.finished_at>NEW.deadline_at THEN
  RAISE EXCEPTION 'late source reversal requires authenticated read' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_reversal_read_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_source_reversal_reads
 FOR EACH ROW EXECUTE FUNCTION protect_seller_source_reversal_read();

CREATE TABLE seller_source_reversal_results (
 command_id uuid PRIMARY KEY REFERENCES seller_source_reversal_commands(id),
 read_id uuid NOT NULL UNIQUE REFERENCES seller_source_reversal_reads(id),
 provider_reversal_id text NOT NULL CHECK(provider_reversal_id ~ '^trr_[A-Za-z0-9_]{6,251}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION protect_seller_source_reversal_result() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE command seller_source_reversal_commands; parent jsonb; observation jsonb;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source reversal result is immutable' USING ERRCODE='55000'; END IF;
 SELECT * INTO STRICT command FROM seller_source_reversal_commands WHERE id=NEW.command_id;
 PERFORM lock_seller_payout_disposition(command.payout_request_id);
 SELECT evidence->'transfer',evidence->'observations'->0 INTO parent,observation
 FROM seller_source_reversal_reads WHERE id=NEW.read_id AND command_id=NEW.command_id
 AND finished_at IS NOT NULL AND outcome='found' AND NOT requires_review AND error_code=''
 AND jsonb_array_length(evidence->'observations')=1;
 IF NOT (
  observation->>'providerId'=NEW.provider_reversal_id AND observation->>'providerTransferId'=command.provider_transfer_id
  AND observation->>'commandId'=command.id::text AND (observation->>'amountCents')::integer=command.amount_cents
  AND observation->>'currency'=command.currency
  AND (observation->>'createdAt')::timestamptz BETWEEN command.created_at-interval '5 minutes' AND command.created_at+interval '23 hours 5 minutes'
  AND (observation->>'createdAt')::timestamptz<=clock_timestamp()+interval '1 minute'
  AND parent->>'ProviderID'=command.provider_transfer_id AND parent->>'DestinationID'=command.destination_id
  AND (parent->>'AmountCents')::integer=command.amount_cents AND parent->>'Currency'=command.currency
  AND parent->>'paymentId'=command.payment_id::text AND parent->>'providerChargeId'=command.provider_charge_id
  AND parent->'liveMode'=to_jsonb(command.live_mode) AND parent->>'TransferGroup'='hcai_'||command.payment_id::text
  AND (parent->>'amountReversed')::integer=command.amount_cents
  AND (parent->>'createdAt')::timestamptz>to_timestamp(0)
  AND (parent->>'createdAt')::timestamptz<=command.created_at+interval '5 minutes'
  AND (parent->>'createdAt')::timestamptz<=(observation->>'createdAt')::timestamptz
 ) IS TRUE OR EXISTS(SELECT 1 FROM seller_source_reversal_reads r WHERE r.command_id=command.id AND r.finished_at IS NOT NULL
  AND (r.requires_review OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.evidence->'observations') o
   WHERE o->>'providerId' IS DISTINCT FROM NEW.provider_reversal_id))) THEN
  RAISE EXCEPTION 'source reversal result lacks matching complete evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_reversal_result_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_source_reversal_results
 FOR EACH ROW EXECUTE FUNCTION protect_seller_source_reversal_result();

CREATE TABLE seller_source_reversal_checks (
 job_id uuid PRIMARY KEY REFERENCES jobs(id),
 command_id uuid NOT NULL REFERENCES seller_source_reversal_dispatches(command_id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX seller_source_reversal_checks_command_idx ON seller_source_reversal_checks(command_id,created_at,job_id);
CREATE FUNCTION protect_seller_source_reversal_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source reversal check is immutable' USING ERRCODE='55000'; END IF;
 IF NOT EXISTS(SELECT 1 FROM seller_source_reversal_dispatches d JOIN jobs j ON j.id=NEW.job_id
 WHERE d.command_id=NEW.command_id AND j.kind='payment.check_seller_source_reversal'
 AND j.payload=jsonb_build_object('commandId',NEW.command_id) AND j.status='queued') THEN
  RAISE EXCEPTION 'source reversal check requires original first-send evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_reversal_check_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_source_reversal_checks
 FOR EACH ROW EXECUTE FUNCTION protect_seller_source_reversal_check();
CREATE VIEW seller_source_reversal_check_candidates AS
 SELECT c.id AS command_id,c.payment_id,
 GREATEST(d.started_at,j.updated_at,last_check.created_at,last_job.updated_at,last_read.finished_at,last_read.deadline_at)+interval '5 minutes' AS due_at
 FROM seller_source_reversal_commands c JOIN seller_source_reversal_dispatches d ON d.command_id=c.id
 JOIN jobs j ON j.id=c.job_id
 LEFT JOIN LATERAL(SELECT x.* FROM seller_source_reversal_checks x WHERE x.command_id=c.id ORDER BY x.created_at DESC,x.job_id DESC LIMIT 1) last_check ON true
 LEFT JOIN jobs last_job ON last_job.id=last_check.job_id
 LEFT JOIN LATERAL(SELECT max(finished_at) AS finished_at,max(deadline_at) AS deadline_at FROM seller_source_reversal_reads WHERE command_id=c.id) last_read ON true
 WHERE j.status IN ('failed','cancelled','succeeded')
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_results WHERE command_id=c.id)
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_checks x JOIN jobs active ON active.id=x.job_id
 WHERE x.command_id=c.id AND active.status IN ('queued','running'));

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation','product_settlement_reconciliation',
 'seller_funding_reconciliation','seller_bank_reconciliation','seller_reversal_reconciliation'));
