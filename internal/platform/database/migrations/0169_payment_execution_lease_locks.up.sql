-- Drain old payment writers before upgrading. Execution authorization uses
-- a separate control row so holding it across a provider call does not block
-- worker lease renewal on jobs. State/kind/payload changes still serialize.
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE payment_job_execution_locks (
 job_id uuid PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0)
);
INSERT INTO payment_job_execution_locks(job_id)
 SELECT id FROM jobs WHERE kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout');

CREATE FUNCTION protect_payment_job_execution_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  PERFORM 1 FROM payment_job_execution_locks WHERE job_id=OLD.id FOR UPDATE;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.kind,NEW.payload,NEW.status) IS NOT DISTINCT FROM ROW(OLD.kind,OLD.payload,OLD.status) THEN
   RETURN NEW;
  END IF;
  IF OLD.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout')
   OR NEW.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout') THEN
   -- Change the tuple as well as locking it. A stale Repeatable Read or
   -- Serializable sender must fail serialization instead of seeing a job
   -- snapshot from before a committed cancellation or payload change.
   INSERT INTO payment_job_execution_locks(job_id) VALUES(NEW.id)
    ON CONFLICT(job_id) DO UPDATE SET revision=payment_job_execution_locks.revision+1;
  END IF;
 ELSE
  IF NEW.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout') THEN
   INSERT INTO payment_job_execution_locks(job_id) VALUES(NEW.id);
  END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER payment_job_execution_insert AFTER INSERT ON jobs
 FOR EACH ROW EXECUTE FUNCTION protect_payment_job_execution_change();
CREATE TRIGGER payment_job_execution_change BEFORE UPDATE OF kind,payload,status ON jobs
 FOR EACH ROW EXECUTE FUNCTION protect_payment_job_execution_change();
CREATE TRIGGER payment_job_execution_delete BEFORE DELETE ON jobs
 FOR EACH ROW EXECUTE FUNCTION protect_payment_job_execution_change();

CREATE FUNCTION payment_execution_job_matches(execution_job uuid,expected_kind text,expected_payload jsonb)
RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('app.payment_execution_lock_protocol',true) IS DISTINCT FROM 'lock-v1'
 OR expected_kind NOT IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout') THEN
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

CREATE OR REPLACE FUNCTION payment_transfer_job_executable(execution_job uuid,expected_kind text,expected_payload jsonb)
RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('app.payment_transfer_execution_protocol',true) IS DISTINCT FROM 'job-v1'
 OR expected_kind NOT IN ('payment.settle_product','payment.fund_seller_payout') THEN
  RETURN false;
 END IF;
 RETURN payment_execution_job_matches(execution_job,expected_kind,expected_payload);
END; $$;

CREATE OR REPLACE FUNCTION assert_seller_bank_execution_job(command uuid, execution uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE continuation_actor uuid;
BEGIN
 IF current_setting('app.seller_bank_resume_protocol',true) IS DISTINCT FROM 'resume-v1' THEN
  RAISE EXCEPTION 'current bank execution protocol required' USING ERRCODE='23514';
 END IF;
 IF NOT payment_execution_job_matches(execution,'payment.execute_seller_bank_payout',jsonb_build_object('commandId',command)) THEN
  RAISE EXCEPTION 'bank execution job stopped or invalid' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM seller_bank_payout_active_dispatches d
 WHERE d.command_id=command AND d.execution_job_id=execution;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'bank execution job stopped or superseded' USING ERRCODE='23514';
 END IF;
 SELECT r.actor_id INTO continuation_actor FROM seller_bank_payout_active_dispatches d
 JOIN seller_bank_payout_resumes r ON r.id=d.resume_id WHERE d.command_id=command;
 IF continuation_actor IS NOT NULL THEN
  PERFORM 1 FROM users u JOIN role_permissions p ON p.role=u.role
  JOIN seller_bank_payout_commands c ON c.id=command
  WHERE u.id=continuation_actor AND u.status='active' AND p.permission_id='admin:finance'
  AND u.id<>c.seller_id FOR SHARE OF u,p;
  IF NOT FOUND THEN
   RAISE EXCEPTION 'bank continuation authority revoked' USING ERRCODE='23514';
  END IF;
 END IF;
END; $$;
