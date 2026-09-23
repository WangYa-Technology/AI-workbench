LOCK TABLE seller_bank_payout_resumes IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_bank_payout_resumes) THEN
  RAISE EXCEPTION 'bank continuation evidence must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
CREATE OR REPLACE VIEW seller_bank_payout_check_candidates AS
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
 END IF;
 RETURN NEW;
END; $$;

DROP FUNCTION assert_seller_bank_execution_job(uuid,uuid);
DROP VIEW seller_bank_payout_active_dispatches;
DROP TABLE seller_bank_payout_resumes;
DROP FUNCTION protect_seller_bank_resume();
