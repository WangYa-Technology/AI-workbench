CREATE TABLE account_deletion_recoveries (
 original_job_id uuid PRIMARY KEY REFERENCES jobs(id),
 retry_job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 request_id uuid NOT NULL REFERENCES data_rights_requests(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 expected_attempts integer NOT NULL CHECK(expected_attempts>=0),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 10 AND 2000),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(original_job_id<>retry_job_id)
);
CREATE FUNCTION reject_account_deletion_recovery_mutation() RETURNS trigger AS $$
BEGIN
 RAISE EXCEPTION 'account deletion recovery evidence is immutable';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER account_deletion_recovery_immutable BEFORE UPDATE OR DELETE ON account_deletion_recoveries
 FOR EACH ROW EXECUTE FUNCTION reject_account_deletion_recovery_mutation();
CREATE INDEX account_deletion_jobs_request ON jobs ((payload->>'requestId'),created_at DESC,id DESC)
 WHERE kind='data_rights.delete';

-- A failed job does not change the request's irreversible stage. A processing
-- request requires the original committed preparation evidence and deleted user.
CREATE VIEW account_deletion_job_policy AS
 SELECT j.id,r.id AS request_id,r.user_id,
 CASE WHEN r.status IN ('processing','completed') THEN 'cleanup' ELSE 'prepare' END AS kind,
 j.status,j.attempts,j.max_attempts,j.last_error_code,
 recovery.retry_job_id,parent.original_job_id AS retry_of,
 CASE WHEN j.status<>'failed' THEN 'not_failed'
 WHEN recovery.retry_job_id IS NOT NULL THEN 'already_retried'
 WHEN r.id IS NULL OR r.request_type<>'account_deletion' THEN 'missing_subject'
 WHEN r.status NOT IN ('scheduled','blocked','processing') OR receipt.request_id IS NOT NULL THEN 'request_closed'
 WHEN r.status='processing' AND (u.status<>'deleted' OR NOT EXISTS(
   SELECT 1 FROM data_rights_events e WHERE e.request_id=r.id AND e.event_type='deletion_prepared' AND e.to_status='processing')) THEN 'inconsistent_stage'
 WHEN r.status<>'processing' AND u.status='deleted' THEN 'inconsistent_stage'
 WHEN r.execute_after>now() OR r.cancel_until>now() THEN 'grace_period'
 WHEN EXISTS(SELECT 1 FROM data_rights_legal_holds h WHERE h.user_id=r.user_id AND h.status='active' AND h.expires_at>now()) THEN 'legal_hold'
 WHEN EXISTS(SELECT 1 FROM jobs active WHERE active.kind=j.kind
   AND active.payload->>'requestId'=r.id::text AND active.status IN ('queued','running')) THEN 'active_job'
 ELSE '' END AS unavailable_reason,j.created_at,j.updated_at
 FROM jobs j
 LEFT JOIN data_rights_requests r ON r.id::text=j.payload->>'requestId'
 LEFT JOIN users u ON u.id=r.user_id
 LEFT JOIN data_rights_deletion_receipts receipt ON receipt.request_id=r.id
 LEFT JOIN account_deletion_recoveries recovery ON recovery.original_job_id=j.id
 LEFT JOIN account_deletion_recoveries parent ON parent.retry_job_id=j.id
 WHERE j.kind='data_rights.delete';
