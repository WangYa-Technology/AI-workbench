DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM job_attempts) THEN
    RAISE EXCEPTION '0031 cannot be rolled back after durable job attempt evidence exists';
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS job_attempts_evidence_guard ON job_attempts;
DROP FUNCTION IF EXISTS protect_job_attempt_evidence();
DROP TABLE IF EXISTS job_attempts;
ALTER TABLE jobs DROP COLUMN last_error_code,DROP COLUMN lease_token;
