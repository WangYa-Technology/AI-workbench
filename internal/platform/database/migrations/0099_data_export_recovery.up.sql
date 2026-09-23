CREATE TABLE data_export_recoveries (
 original_job_id uuid PRIMARY KEY REFERENCES jobs(id),
 retry_job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 request_id uuid NOT NULL REFERENCES data_rights_requests(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 expected_attempts integer NOT NULL CHECK(expected_attempts>=0),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 10 AND 2000),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(original_job_id<>retry_job_id)
);
CREATE FUNCTION reject_data_export_recovery_mutation() RETURNS trigger AS $$
BEGIN
 RAISE EXCEPTION 'export recovery evidence is immutable';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER data_export_recovery_immutable BEFORE UPDATE OR DELETE ON data_export_recoveries
 FOR EACH ROW EXECUTE FUNCTION reject_data_export_recovery_mutation();
CREATE INDEX data_export_jobs_request ON jobs ((payload->>'requestId'),kind,created_at DESC,id DESC)
 WHERE kind IN ('data_rights.export','data_rights.export_expire');

-- Project durable job failure without manufacturing a successful export or
-- mutating a queued request during a GET. Active replacements take precedence.
CREATE VIEW data_export_execution AS
 SELECT r.id AS request_id,j.status AS job_status,j.last_error_code,j.updated_at
 FROM data_rights_requests r
 LEFT JOIN LATERAL (
  SELECT status,last_error_code,updated_at FROM jobs
  WHERE kind='data_rights.export' AND payload->>'requestId'=r.id::text
  ORDER BY (status IN ('queued','running')) DESC,created_at DESC,id DESC LIMIT 1
 ) j ON true
 WHERE r.request_type='data_export';

CREATE VIEW data_export_job_policy AS
 SELECT j.id,r.id AS request_id,r.user_id,
 CASE WHEN j.kind='data_rights.export' THEN 'export' ELSE 'expiry' END AS kind,
 j.status,j.attempts,j.max_attempts,j.last_error_code,
 recovery.retry_job_id,parent.original_job_id AS retry_of,
 CASE WHEN j.status<>'failed' THEN 'not_failed'
 WHEN recovery.retry_job_id IS NOT NULL THEN 'already_retried'
 WHEN r.id IS NULL OR r.request_type<>'data_export' THEN 'missing_subject'
 WHEN j.kind='data_rights.export' AND (r.status<>'queued' OR a.request_id IS NOT NULL) THEN 'request_closed'
 WHEN j.kind='data_rights.export' AND u.status<>'active' THEN 'owner_inactive'
 WHEN j.kind='data_rights.export_expire' AND (a.request_id IS NULL OR r.status<>'ready') THEN 'request_closed'
 WHEN j.kind='data_rights.export_expire' AND a.purged_at IS NOT NULL THEN 'already_purged'
 WHEN j.kind='data_rights.export_expire' AND a.expires_at>now() THEN 'not_expired'
 WHEN EXISTS(SELECT 1 FROM jobs active WHERE active.kind=j.kind
   AND active.payload->>'requestId'=r.id::text AND active.status IN ('queued','running')) THEN 'active_job'
 ELSE '' END AS unavailable_reason,j.created_at,j.updated_at
 FROM jobs j
 LEFT JOIN data_rights_requests r ON r.id::text=j.payload->>'requestId'
 LEFT JOIN users u ON u.id=r.user_id
 LEFT JOIN data_rights_export_artifacts a ON a.request_id=r.id
 LEFT JOIN data_export_recoveries recovery ON recovery.original_job_id=j.id
 LEFT JOIN data_export_recoveries parent ON parent.retry_job_id=j.id
 WHERE j.kind IN ('data_rights.export','data_rights.export_expire');
