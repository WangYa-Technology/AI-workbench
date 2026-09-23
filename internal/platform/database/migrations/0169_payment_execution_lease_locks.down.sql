-- Drain matching writers/workers before rollback. Financial commands, results
-- and dispatch evidence are retained. The older functions again hold jobs row
-- locks and can block heartbeats, so this is not a live safe downgrade.
CREATE OR REPLACE FUNCTION assert_seller_bank_execution_job(command uuid, execution uuid) RETURNS void LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION payment_transfer_job_executable(execution_job uuid, expected_kind text, expected_payload jsonb)
RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('app.payment_transfer_execution_protocol',true) IS DISTINCT FROM 'job-v1'
 OR expected_kind NOT IN ('payment.settle_product','payment.fund_seller_payout') THEN
  RETURN false;
 END IF;
 PERFORM 1 FROM jobs j WHERE j.id=execution_job AND j.kind=expected_kind
 AND j.payload=expected_payload AND j.status IN ('queued','running') FOR SHARE OF j;
 RETURN FOUND;
END; $$;

DROP TRIGGER payment_job_execution_insert ON jobs;
DROP TRIGGER payment_job_execution_change ON jobs;
DROP TRIGGER payment_job_execution_delete ON jobs;
DROP FUNCTION protect_payment_job_execution_change();
DROP FUNCTION payment_execution_job_matches(uuid,text,jsonb);
DROP TABLE payment_job_execution_locks;
