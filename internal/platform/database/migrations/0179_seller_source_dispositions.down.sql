DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_source_reversal_dispositions) THEN RAISE EXCEPTION 'source dispositions must be retained' USING ERRCODE='55000'; END IF;
END $$;
DROP TRIGGER source_closure_reviewer ON seller_source_reversal_closures;
DROP FUNCTION protect_source_closure_reviewer();
DROP FUNCTION assert_seller_source_reversal_execution(uuid,uuid);
ALTER FUNCTION assert_seller_source_reversal_execution_0171(uuid,uuid) RENAME TO assert_seller_source_reversal_execution;
ALTER TABLE seller_source_reversal_reads DROP CONSTRAINT seller_source_reversal_reads_command_id_fkey;
ALTER TABLE seller_source_reversal_reads ADD CONSTRAINT seller_source_reversal_reads_command_id_fkey FOREIGN KEY(command_id) REFERENCES seller_source_reversal_dispatches(command_id);
ALTER TABLE seller_source_reversal_checks DROP CONSTRAINT seller_source_reversal_checks_command_id_fkey;
ALTER TABLE seller_source_reversal_checks ADD CONSTRAINT seller_source_reversal_checks_command_id_fkey FOREIGN KEY(command_id) REFERENCES seller_source_reversal_dispatches(command_id);
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
CREATE OR REPLACE FUNCTION protect_seller_source_reversal_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'source reversal check is immutable' USING ERRCODE='55000'; END IF;
 IF NOT EXISTS(SELECT 1 FROM seller_source_reversal_dispatches d JOIN jobs j ON j.id=NEW.job_id
 WHERE d.command_id=NEW.command_id AND j.kind='payment.check_seller_source_reversal'
 AND j.payload=jsonb_build_object('commandId',NEW.command_id) AND j.status='queued') THEN
  RAISE EXCEPTION 'source reversal check requires original first-send evidence' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE OR REPLACE VIEW seller_source_reversal_check_candidates AS
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

DROP TABLE seller_source_reversal_dispositions;
DROP FUNCTION check_source_disposition_audit();
DROP FUNCTION protect_seller_source_disposition();
DROP FUNCTION seller_source_disposition_snapshot(uuid);

CREATE OR REPLACE FUNCTION seller_source_reversal_close_resolution(command uuid) RETURNS text LANGUAGE sql STABLE AS $$
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

