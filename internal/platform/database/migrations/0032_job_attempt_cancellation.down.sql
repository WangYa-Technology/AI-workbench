DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM job_attempts WHERE status='cancelled') THEN
    RAISE EXCEPTION '0032 cannot be rolled back after cancelled job attempt evidence exists';
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS jobs_close_cancelled_attempt ON jobs;
DROP FUNCTION IF EXISTS close_cancelled_job_attempt();
ALTER TABLE job_attempts DROP CONSTRAINT job_attempts_terminal_state_check;
ALTER TABLE job_attempts ADD CONSTRAINT job_attempts_check CHECK (
  (status='running' AND finished_at IS NULL AND error_code IS NULL) OR
  (status='succeeded' AND finished_at IS NOT NULL AND error_code IS NULL) OR
  (status IN ('retry_scheduled','failed','lease_expired') AND finished_at IS NOT NULL AND error_code IS NOT NULL)
);
ALTER TABLE job_attempts DROP CONSTRAINT job_attempts_status_check;
ALTER TABLE job_attempts ADD CONSTRAINT job_attempts_status_check
  CHECK (status IN ('running','succeeded','retry_scheduled','failed','lease_expired'));
