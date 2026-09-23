DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM legal_hold_cleanup_checks) OR EXISTS(SELECT 1 FROM legal_hold_cleanup_dispatches) THEN
  RAISE EXCEPTION 'cannot discard legal hold cleanup evidence';
 END IF;
END $$;
DROP INDEX legal_hold_closed_scan;
DROP TABLE legal_hold_cleanup_dispatches;
DROP TABLE legal_hold_cleanup_checks;
DROP FUNCTION guard_legal_hold_cleanup_check();
