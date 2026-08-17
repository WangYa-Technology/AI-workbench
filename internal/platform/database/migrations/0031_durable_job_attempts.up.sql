ALTER TABLE jobs
  ADD COLUMN lease_token uuid,
  ADD COLUMN last_error_code text CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z0-9_]{3,80}$');

UPDATE jobs
SET status=CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
    available_at=now(),lease_owner=NULL,lease_expires_at=NULL,
    last_error='migration_restart_recovery',last_error_code='migration_restart_recovery',updated_at=now()
WHERE status='running';

CREATE TABLE job_attempts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  attempt_number integer NOT NULL CHECK (attempt_number > 0),
  job_kind text NOT NULL CHECK (char_length(job_kind) BETWEEN 3 AND 120),
  worker_ref_hash text NOT NULL CHECK (worker_ref_hash ~ '^[0-9a-f]{64}$'),
  lease_token uuid NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'running' CHECK (status IN ('running','succeeded','retry_scheduled','failed','lease_expired')),
  lease_renewals integer NOT NULL DEFAULT 0 CHECK (lease_renewals >= 0),
  error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z0-9_]{3,80}$'),
  lease_expires_at timestamptz NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(job_id,attempt_number),
  CHECK ((status='running' AND finished_at IS NULL AND error_code IS NULL) OR
         (status='succeeded' AND finished_at IS NOT NULL AND error_code IS NULL) OR
         (status IN ('retry_scheduled','failed','lease_expired') AND finished_at IS NOT NULL AND error_code IS NOT NULL))
);

CREATE INDEX job_attempts_recent_idx ON job_attempts(started_at DESC,id DESC);
CREATE INDEX job_attempts_status_idx ON job_attempts(status,started_at DESC);

CREATE FUNCTION protect_job_attempt_evidence() RETURNS trigger AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'job attempt evidence is immutable';
  END IF;
  IF OLD.status <> 'running' THEN
    RAISE EXCEPTION 'terminal job attempt evidence is immutable';
  END IF;
  IF NEW.id <> OLD.id OR NEW.job_id <> OLD.job_id OR NEW.attempt_number <> OLD.attempt_number OR
     NEW.job_kind <> OLD.job_kind OR NEW.worker_ref_hash <> OLD.worker_ref_hash OR
     NEW.lease_token <> OLD.lease_token OR NEW.started_at <> OLD.started_at OR
     NEW.lease_renewals < OLD.lease_renewals OR NEW.lease_expires_at < OLD.lease_expires_at THEN
    RAISE EXCEPTION 'job attempt identity evidence is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER job_attempts_evidence_guard
BEFORE UPDATE OR DELETE ON job_attempts
FOR EACH ROW EXECUTE FUNCTION protect_job_attempt_evidence();
