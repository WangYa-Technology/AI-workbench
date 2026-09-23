-- Explicit, audited continuation only before the first bank send, within the
-- original command window. Drain older API/worker binaries before upgrading.
CREATE TABLE seller_bank_payout_resumes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 command_id uuid NOT NULL REFERENCES seller_bank_payout_dispatches(command_id),
 revision integer NOT NULL CHECK(revision>0),
 predecessor_job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 reason text NOT NULL CHECK(char_length(btrim(reason)) BETWEEN 10 AND 1000),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 UNIQUE(command_id,revision), UNIQUE(actor_id,idempotency_key), CHECK(job_id<>predecessor_job_id)
);
CREATE VIEW seller_bank_payout_active_dispatches AS
 SELECT d.*,COALESCE(r.job_id,d.job_id) AS execution_job_id,
 r.id AS resume_id,COALESCE(r.revision,0) AS resume_revision
 FROM seller_bank_payout_dispatches d
 LEFT JOIN LATERAL (SELECT id,job_id,revision FROM seller_bank_payout_resumes WHERE command_id=d.command_id
 ORDER BY revision DESC LIMIT 1) r ON true;

CREATE FUNCTION assert_seller_bank_execution_job(command uuid, execution uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE continuation_actor uuid;
BEGIN
 IF current_setting('app.seller_bank_resume_protocol',true) IS DISTINCT FROM 'resume-v1' THEN
  RAISE EXCEPTION 'current bank execution protocol required' USING ERRCODE='23514';
 END IF;
 -- Hold the job lock through the first-send marker or the actual first call.
 -- A stopped or superseded job cannot borrow another continuation's authority.
 PERFORM 1 FROM seller_bank_payout_active_dispatches d JOIN jobs j ON j.id=d.execution_job_id
 WHERE d.command_id=command AND j.id=execution AND j.status IN ('queued','running')
 AND j.kind='payment.execute_seller_bank_payout'
 AND j.payload=jsonb_build_object('commandId',command) FOR SHARE OF j;
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

CREATE FUNCTION protect_seller_bank_resume() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE command seller_bank_payout_commands; current_job uuid; revision integer; start_time timestamptz;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'bank resume evidence is immutable' USING ERRCODE='55000'; END IF;
 IF current_setting('app.seller_bank_resume_protocol',true) IS DISTINCT FROM 'resume-v1' THEN
  RAISE EXCEPTION 'bank resume protocol required' USING ERRCODE='23514';
 END IF;
 SELECT * INTO STRICT command FROM seller_bank_payout_commands WHERE id=NEW.command_id;
 PERFORM assert_seller_bank_payout_command(command);
 PERFORM 1 FROM users u JOIN role_permissions p ON p.role=u.role
 WHERE u.id=NEW.actor_id AND u.status='active' AND p.permission_id='admin:finance' FOR SHARE OF u,p;
 IF NOT FOUND OR NEW.actor_id=command.seller_id THEN
  RAISE EXCEPTION 'bank resume requires current independent finance authority' USING ERRCODE='23514';
 END IF;
 SELECT d.execution_job_id,d.resume_revision,d.started_at INTO STRICT current_job,revision,start_time
 FROM seller_bank_payout_active_dispatches d WHERE d.command_id=NEW.command_id;
 PERFORM 1 FROM jobs WHERE id=current_job AND status IN ('failed','cancelled','succeeded') FOR UPDATE;
 IF NOT FOUND OR NEW.predecessor_job_id<>current_job OR NEW.revision<>revision+1 OR start_time IS NOT NULL
 OR clock_timestamp()>=command.created_at+interval '23 hours'
 OR NEW.created_at>clock_timestamp() OR NEW.created_at<clock_timestamp()-interval '1 minute'
 OR EXISTS(SELECT 1 FROM seller_bank_payout_reads WHERE command_id=command.id)
 OR EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=command.id)
 OR EXISTS(SELECT 1 FROM seller_bank_payout_dispatches WHERE command_id=command.id AND job_id=NEW.job_id)
 OR NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id AND status='queued' AND kind='payment.execute_seller_bank_payout'
 AND payload=jsonb_build_object('commandId',NEW.command_id)) THEN
  RAISE EXCEPTION 'bank resume requires an unstarted stopped original operation within its window' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER seller_bank_resume_guard BEFORE INSERT OR UPDATE OR DELETE ON seller_bank_payout_resumes
 FOR EACH ROW EXECUTE FUNCTION protect_seller_bank_resume();

CREATE OR REPLACE FUNCTION protect_seller_bank_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
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
  PERFORM assert_seller_bank_execution_job(NEW.command_id,NULLIF(current_setting('app.seller_bank_execution_job',true),'')::uuid);
 END IF;
 RETURN NEW;
END; $$;

CREATE OR REPLACE VIEW seller_bank_payout_check_candidates AS
 SELECT d.command_id,c.payment_id,
 GREATEST(d.started_at,j.updated_at,last_check.created_at,last_job.updated_at,last_read.finished_at)
  + CASE WHEN EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=c.id AND status='paid')
    THEN interval '24 hours' ELSE interval '5 minutes' END AS due_at
 FROM seller_bank_payout_active_dispatches d JOIN seller_bank_payout_commands c ON c.id=d.command_id
 JOIN jobs j ON j.id=d.execution_job_id
 LEFT JOIN LATERAL (SELECT x.* FROM seller_bank_payout_checks x WHERE x.command_id=d.command_id
 ORDER BY x.created_at DESC,x.job_id DESC LIMIT 1) last_check ON true
 LEFT JOIN jobs last_job ON last_job.id=last_check.job_id
 LEFT JOIN LATERAL (SELECT max(r.finished_at) finished_at FROM seller_bank_payout_reads r WHERE r.command_id=d.command_id) last_read ON true
 WHERE d.started_at IS NOT NULL AND j.status IN ('failed','cancelled','succeeded')
 AND NOT EXISTS(SELECT 1 FROM seller_bank_payout_results WHERE command_id=c.id AND status IN ('failed','canceled'))
 AND NOT EXISTS(SELECT 1 FROM seller_bank_payout_checks x JOIN jobs active ON active.id=x.job_id
 WHERE x.command_id=d.command_id AND active.status IN ('queued','running'));
