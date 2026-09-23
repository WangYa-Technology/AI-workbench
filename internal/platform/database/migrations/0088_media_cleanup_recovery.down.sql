DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM media_cleanup_recoveries) THEN
  RAISE EXCEPTION 'cannot discard media cleanup recovery evidence';
 END IF;
END $$;
DROP INDEX media_cleanup_jobs_subject;
DROP TABLE media_cleanup_recoveries;
DROP FUNCTION reject_media_cleanup_recovery_mutation();
