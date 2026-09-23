DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM account_deletion_recoveries) THEN
  RAISE EXCEPTION 'cannot discard account deletion recovery evidence';
 END IF;
END $$;
DROP VIEW account_deletion_job_policy;
DROP INDEX account_deletion_jobs_request;
DROP TABLE account_deletion_recoveries;
DROP FUNCTION reject_account_deletion_recovery_mutation();
