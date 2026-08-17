ALTER TABLE job_attempts DROP CONSTRAINT job_attempts_status_check;
ALTER TABLE job_attempts ADD CONSTRAINT job_attempts_status_check
  CHECK (status IN ('running','succeeded','retry_scheduled','failed','lease_expired','cancelled'));

ALTER TABLE job_attempts DROP CONSTRAINT job_attempts_check;
ALTER TABLE job_attempts ADD CONSTRAINT job_attempts_terminal_state_check CHECK (
  (status='running' AND finished_at IS NULL AND error_code IS NULL) OR
  (status='succeeded' AND finished_at IS NOT NULL AND error_code IS NULL) OR
  (status IN ('retry_scheduled','failed','lease_expired','cancelled') AND finished_at IS NOT NULL AND error_code IS NOT NULL)
);

CREATE FUNCTION close_cancelled_job_attempt() RETURNS trigger AS $$
DECLARE
  closed_attempts integer;
BEGIN
  UPDATE job_attempts
  SET status='cancelled',error_code='job_cancelled',finished_at=now(),updated_at=now()
  WHERE lease_token=OLD.lease_token AND status='running';
  GET DIAGNOSTICS closed_attempts = ROW_COUNT;
  IF closed_attempts <> 1 THEN
    RAISE EXCEPTION 'running job cancellation requires one active attempt';
  END IF;
  NEW.lease_owner := NULL;
  NEW.lease_token := NULL;
  NEW.lease_expires_at := NULL;
  NEW.last_error := 'job_cancelled';
  NEW.last_error_code := 'job_cancelled';
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER jobs_close_cancelled_attempt
BEFORE UPDATE OF status ON jobs
FOR EACH ROW
WHEN (OLD.status='running' AND NEW.status='cancelled')
EXECUTE FUNCTION close_cancelled_job_attempt();
