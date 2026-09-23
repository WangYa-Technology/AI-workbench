DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM data_export_recoveries) THEN
  RAISE EXCEPTION 'cannot discard export recovery evidence';
 END IF;
END $$;
DROP VIEW data_export_job_policy;
DROP VIEW data_export_execution;
DROP INDEX data_export_jobs_request;
DROP TABLE data_export_recoveries;
DROP FUNCTION reject_data_export_recovery_mutation();
