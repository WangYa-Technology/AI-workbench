-- Recover missing execution, never invent a request or reset a failed job.
CREATE VIEW account_deletion_reconciliation_policy AS
 SELECT r.id AS request_id,r.user_id,r.created_at,r.status AS request_status,
 CASE WHEN r.status='processing' THEN 'cleanup' ELSE 'prepare' END AS stage,
 CASE WHEN r.status NOT IN ('scheduled','blocked','processing') OR r.completed_at IS NOT NULL OR EXISTS(
  SELECT 1 FROM data_rights_deletion_receipts receipt WHERE receipt.request_id=r.id) THEN 'request_closed'
 WHEN r.cancel_until IS NULL OR NOT EXISTS(
  SELECT 1 FROM data_rights_events e WHERE e.request_id=r.id AND e.actor_id=r.user_id
   AND e.event_type='requested' AND e.to_status IN ('scheduled','blocked')) THEN 'inconsistent_stage'
 WHEN r.status='processing' AND (u.status<>'deleted' OR NOT EXISTS(
  SELECT 1 FROM data_rights_events e WHERE e.request_id=r.id AND e.event_type='deletion_prepared' AND e.to_status='processing')) THEN 'inconsistent_stage'
 WHEN r.status<>'processing' AND (u.status='deleted' OR EXISTS(
  SELECT 1 FROM data_rights_events e WHERE e.request_id=r.id AND e.event_type='deletion_prepared' AND e.to_status='processing')) THEN 'inconsistent_stage'
 WHEN r.execute_after>now() OR r.cancel_until>now() THEN 'grace_period'
 WHEN EXISTS(SELECT 1 FROM data_rights_legal_holds h WHERE h.user_id=r.user_id AND h.status='active' AND h.expires_at>now()) THEN 'legal_hold'
 ELSE '' END AS unavailable_reason
 FROM data_rights_requests r JOIN users u ON u.id=r.user_id WHERE r.request_type='account_deletion';

CREATE VIEW account_deletion_reconciliation_candidates AS
 SELECT p.request_id,p.user_id,p.created_at,p.stage,last_job.id AS previous_job_id
 FROM account_deletion_reconciliation_policy p
 LEFT JOIN LATERAL (
  SELECT id,status FROM jobs WHERE kind='data_rights.delete' AND payload->>'requestId'=p.request_id::text
  ORDER BY created_at DESC,id DESC LIMIT 1
 ) last_job ON true
 WHERE p.request_status IN ('scheduled','blocked','processing') AND p.unavailable_reason='' AND (last_job.id IS NULL OR last_job.status='succeeded')
 AND NOT EXISTS(SELECT 1 FROM jobs active WHERE active.kind='data_rights.delete'
  AND active.payload->>'requestId'=p.request_id::text AND active.status IN ('queued','running'));

CREATE TABLE account_deletion_reconciliations (
 job_id uuid PRIMARY KEY REFERENCES jobs(id),
 request_id uuid NOT NULL REFERENCES data_rights_requests(id),
 previous_job_id uuid REFERENCES jobs(id),
 stage text NOT NULL CHECK(stage IN ('prepare','cleanup')),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(previous_job_id IS NULL OR previous_job_id<>job_id)
);
CREATE UNIQUE INDEX account_deletion_reconciliation_predecessor ON account_deletion_reconciliations
 (request_id,COALESCE(previous_job_id,'00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX account_deletion_reconciliation_request ON account_deletion_reconciliations(request_id,created_at,job_id);
CREATE INDEX account_deletion_reconciliation_scan ON data_rights_requests(created_at,id)
 WHERE request_type='account_deletion' AND status IN ('scheduled','blocked','processing');

CREATE FUNCTION guard_account_deletion_reconciliation() RETURNS trigger AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'account deletion reconciliation evidence is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id AND kind='data_rights.delete'
  AND payload->>'requestId'=NEW.request_id::text AND status='queued') OR
 NOT EXISTS(SELECT 1 FROM account_deletion_reconciliation_policy WHERE request_id=NEW.request_id
  AND stage=NEW.stage AND unavailable_reason='') OR
 (NEW.previous_job_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.previous_job_id
  AND kind='data_rights.delete' AND payload->>'requestId'=NEW.request_id::text AND status='succeeded')) THEN
  RAISE EXCEPTION 'account deletion reconciliation must link eligible request and matching jobs';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER account_deletion_reconciliation_guard BEFORE INSERT OR UPDATE OR DELETE ON account_deletion_reconciliations
 FOR EACH ROW EXECUTE FUNCTION guard_account_deletion_reconciliation();
