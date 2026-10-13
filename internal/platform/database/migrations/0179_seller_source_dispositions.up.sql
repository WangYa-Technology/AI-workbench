-- Drain financial writers before applying. Disposition never changes money.
CREATE TABLE seller_source_reversal_dispositions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 command_id uuid NOT NULL REFERENCES seller_source_reversal_commands(id),
 phase text NOT NULL CHECK(phase IN ('propose','approve')),
 proposal_id uuid REFERENCES seller_source_reversal_dispositions(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 expected_updated_at timestamptz NOT NULL CHECK(isfinite(expected_updated_at)),
 reason text NOT NULL CHECK(char_length(btrim(reason)) BETWEEN 10 AND 1000),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 request_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((phase='approve')=(proposal_id IS NOT NULL)),
 UNIQUE(actor_id,idempotency_key)
);
CREATE UNIQUE INDEX seller_source_disposition_approved_once ON seller_source_reversal_dispositions(command_id) WHERE phase='approve';
CREATE INDEX seller_source_disposition_history ON seller_source_reversal_dispositions(command_id,created_at,id);

-- Never accepts caller-supplied amounts or Provider facts. Changes to any
-- relevant evidence invalidate an approval, including read-only observations.
CREATE FUNCTION seller_source_disposition_snapshot(command uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('request',to_jsonb(r),'source',to_jsonb(t),'identity',original.identity,
 'payment',jsonb_build_object('status',p.status,'version',p.version,'charge',p.provider_charge_id,'provider',p.provider,'liveMode',p.live_mode,'amount',p.amount_cents,'currency',p.currency),
 'order',jsonb_build_object('status',o.status,'updatedAt',o.updated_at),
 'settlement',to_jsonb(s),'bank',to_jsonb(bank),'job',jsonb_build_object('status',j.status,'attempts',j.attempts),
 'reads',(SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.started_at,x.id),'[]') FROM seller_source_reversal_reads x WHERE x.command_id=c.id),
 'bankReads',(SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.started_at,x.id),'[]') FROM seller_bank_payout_reads x WHERE x.command_id=c.bank_command_id),
 'debt',(SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.id),'[]') FROM seller_recovery_obligations x WHERE x.settlement_id=s.id),
 'allocation',(SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.settlement_id),'[]') FROM seller_payout_request_allocations x WHERE x.payout_request_id=r.id),
 'refunds',(SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.operation_id),'[]') FROM product_refund_attempts x WHERE x.payment_id=p.id))
 FROM seller_source_reversal_commands c JOIN seller_payout_requests r ON r.id=c.payout_request_id
 JOIN seller_payout_transfers t ON t.id=c.source_transfer_id JOIN product_settlements s ON s.id=c.settlement_id
 JOIN payment_intents p ON p.id=c.payment_id JOIN orders o ON o.id=p.order_id
 JOIN product_checkout_requests original ON original.payment_id=p.id JOIN jobs j ON j.id=c.job_id
 LEFT JOIN LATERAL seller_source_reversal_bank_state(r.id) bank ON true WHERE c.id=command;
$$;

CREATE FUNCTION protect_seller_source_disposition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c seller_source_reversal_commands; proposal seller_source_reversal_dispositions;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source disposition is immutable' USING ERRCODE='55000'; END IF;
 SELECT * INTO STRICT c FROM seller_source_reversal_commands WHERE id=NEW.command_id;
 PERFORM lock_seller_payout_disposition(c.payout_request_id);
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role WHERE u.id=NEW.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR NEW.actor_id=c.seller_id OR EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE command_id=c.id)
 OR EXISTS(SELECT 1 FROM seller_source_reversal_dispositions WHERE command_id=c.id AND phase='approve')
 OR NOT EXISTS(SELECT 1 FROM seller_payout_requests WHERE id=c.payout_request_id AND updated_at=NEW.expected_updated_at)
 OR NEW.snapshot IS DISTINCT FROM seller_source_disposition_snapshot(c.id) THEN
 RAISE EXCEPTION 'source disposition requires current independent finance and evidence' USING ERRCODE='23514'; END IF;
 IF NEW.phase='approve' THEN
 SELECT * INTO STRICT proposal FROM seller_source_reversal_dispositions WHERE id=NEW.proposal_id AND command_id=c.id AND phase='propose';
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role WHERE u.id=proposal.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR proposal.actor_id=NEW.actor_id OR proposal.snapshot IS DISTINCT FROM NEW.snapshot
 OR clock_timestamp()>=proposal.created_at+interval '24 hours' THEN
 RAISE EXCEPTION 'source disposition requires another current reviewer and unchanged evidence' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_source_disposition_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_source_reversal_dispositions FOR EACH ROW EXECUTE FUNCTION protect_seller_source_disposition();

ALTER FUNCTION assert_seller_source_reversal_execution(uuid,uuid) RENAME TO assert_seller_source_reversal_execution_0171;
CREATE FUNCTION assert_seller_source_reversal_execution(command uuid, execution uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM seller_source_reversal_dispositions WHERE command_id=command) THEN
 RAISE EXCEPTION 'source command permanently restricted to reads' USING ERRCODE='23514'; END IF;
 PERFORM assert_seller_source_reversal_execution_0171(command,execution);
END; $$;

-- Read journals no longer require a fabricated first-send marker. An approved
-- independent disposition is the only extra authorization for an unsent read.
ALTER TABLE seller_source_reversal_reads DROP CONSTRAINT seller_source_reversal_reads_command_id_fkey;
ALTER TABLE seller_source_reversal_reads ADD CONSTRAINT seller_source_reversal_reads_command_id_fkey FOREIGN KEY(command_id) REFERENCES seller_source_reversal_commands(id);
ALTER TABLE seller_source_reversal_checks DROP CONSTRAINT seller_source_reversal_checks_command_id_fkey;
ALTER TABLE seller_source_reversal_checks ADD CONSTRAINT seller_source_reversal_checks_command_id_fkey FOREIGN KEY(command_id) REFERENCES seller_source_reversal_commands(id);
CREATE OR REPLACE FUNCTION protect_seller_source_reversal_read() RETURNS trigger LANGUAGE plpgsql AS $$
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
  OR NOT (EXISTS(SELECT 1 FROM seller_source_reversal_dispatches WHERE command_id=NEW.command_id) OR (NEW.kind='query' AND EXISTS(SELECT 1 FROM seller_source_reversal_dispositions WHERE command_id=NEW.command_id AND phase='approve')))
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
CREATE OR REPLACE FUNCTION protect_seller_source_reversal_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source reversal check is immutable' USING ERRCODE='55000'; END IF;
 IF NOT EXISTS(SELECT 1 FROM seller_source_reversal_commands d JOIN jobs j ON j.id=NEW.job_id
 WHERE d.id=NEW.command_id AND (EXISTS(SELECT 1 FROM seller_source_reversal_dispatches WHERE command_id=d.id) OR EXISTS(SELECT 1 FROM seller_source_reversal_dispositions WHERE command_id=d.id AND phase='approve')) AND j.kind='payment.check_seller_source_reversal'
 AND j.payload=jsonb_build_object('commandId',NEW.command_id) AND j.status='queued') THEN
  RAISE EXCEPTION 'source reversal check requires original first-send evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE OR REPLACE VIEW seller_source_reversal_check_candidates AS
 SELECT c.id AS command_id,c.payment_id,
 GREATEST(c.created_at,d.started_at,j.updated_at,last_check.created_at,last_job.updated_at,last_read.finished_at,last_read.deadline_at)+interval '5 minutes' AS due_at
 FROM seller_source_reversal_commands c LEFT JOIN seller_source_reversal_dispatches d ON d.command_id=c.id
 JOIN jobs j ON j.id=c.job_id
 LEFT JOIN LATERAL(SELECT x.* FROM seller_source_reversal_checks x WHERE x.command_id=c.id ORDER BY x.created_at DESC,x.job_id DESC LIMIT 1) last_check ON true
 LEFT JOIN jobs last_job ON last_job.id=last_check.job_id
 LEFT JOIN LATERAL(SELECT max(finished_at) AS finished_at,max(deadline_at) AS deadline_at FROM seller_source_reversal_reads WHERE command_id=c.id) last_read ON true
 WHERE (j.status IN ('failed','cancelled','succeeded') OR EXISTS(SELECT 1 FROM seller_source_reversal_dispositions WHERE command_id=c.id AND phase='approve')) AND (d.command_id IS NOT NULL OR EXISTS(SELECT 1 FROM seller_source_reversal_dispositions WHERE command_id=c.id AND phase='approve'))
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_closures WHERE command_id=c.id) AND (NOT EXISTS(SELECT 1 FROM seller_source_reversal_results WHERE command_id=c.id) OR NOT seller_source_reversal_return_proven(c.id))
 AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_checks x JOIN jobs active ON active.id=x.job_id
 WHERE x.command_id=c.id AND active.status IN ('queued','running'));


-- Maker/checker separation applies even to complete returns. Provider proof,
-- bank/debt/allocation guards from 0172 remain mandatory and unchanged.
CREATE FUNCTION protect_source_closure_reviewer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM seller_source_reversal_commands WHERE id=NEW.command_id AND actor_id=NEW.actor_id)
 OR EXISTS(SELECT 1 FROM seller_source_reversal_dispositions WHERE command_id=NEW.command_id AND phase='propose' AND actor_id=NEW.actor_id) THEN
 RAISE EXCEPTION 'source closure requires independent reviewer' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER source_closure_reviewer BEFORE INSERT ON seller_source_reversal_closures FOR EACH ROW EXECUTE FUNCTION protect_source_closure_reviewer();

CREATE FUNCTION check_source_disposition_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM audit_events WHERE actor_id=NEW.actor_id AND action='seller_payout.source_disposition_'||NEW.phase AND metadata->>'dispositionId'=NEW.id::text)
 OR (NEW.phase='approve' AND NOT EXISTS(SELECT 1 FROM seller_source_reversal_checks x JOIN jobs j ON j.id=x.job_id WHERE x.command_id=NEW.command_id AND j.created_at>=NEW.created_at)) THEN
 RAISE EXCEPTION 'source disposition must commit audit and approved read job' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE CONSTRAINT TRIGGER source_disposition_audited AFTER INSERT ON seller_source_reversal_dispositions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_source_disposition_audit();

CREATE OR REPLACE FUNCTION seller_source_reversal_close_resolution(command uuid) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN p.status='paid' AND o.status='fulfilled' AND s.status='available'
  AND NOT EXISTS(SELECT 1 FROM seller_recovery_obligations d WHERE d.settlement_id=s.id AND (d.remaining_cents>0 OR d.status IN ('open','reconciliation_required')))
  THEN 'released'
 WHEN p.status='refunded' AND o.status='refunded' AND s.status='recovery_required' AND s.hold_reason IN ('buyer_refund','provider_dispute_loss')
  AND s.recovery_amount_cents=c.amount_cents
  AND EXISTS(SELECT 1 FROM seller_recovery_obligations d WHERE d.settlement_id=s.id AND d.seller_id=c.seller_id
   AND d.currency=c.currency AND d.amount_cents=c.amount_cents AND d.remaining_cents=c.amount_cents AND d.status IN ('open','reconciliation_required'))
  AND ((s.hold_reason='buyer_refund' AND (SELECT count(*) FROM product_refund_attempts a WHERE a.payment_id=p.id AND a.status='succeeded')=1
  AND EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=p.id AND a.status='succeeded'
   AND a.provider=p.provider AND a.provider_payment_id=p.provider_payment_id AND a.amount_cents=p.amount_cents AND a.currency=p.currency AND NOT a.reconciliation_required)) OR (s.hold_reason='provider_dispute_loss' AND EXISTS(SELECT 1 FROM product_payment_dispute_adjudications a JOIN product_payment_disputes d ON d.id=a.dispute_id WHERE d.payment_id=p.id AND a.financial_effect='loss_applied' AND a.outcome IN ('lost','charge_refunded') AND d.action_status IN ('lost','charge_refunded')) AND NOT EXISTS(SELECT 1 FROM product_payment_disputes d WHERE d.payment_id=p.id AND d.action_status NOT IN ('won','lost','charge_refunded'))))
  THEN 'refund_recovered' END
 FROM seller_source_reversal_commands c JOIN product_settlements s ON s.id=c.settlement_id
 JOIN payment_intents p ON p.id=c.payment_id JOIN orders o ON o.id=s.order_id
 WHERE c.id=command
 AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=p.id AND (a.status IN ('requested','pending') OR a.reconciliation_required));
$$;

