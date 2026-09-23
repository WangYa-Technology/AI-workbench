DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM account_deletion_reconciliations) THEN
  RAISE EXCEPTION 'cannot discard account deletion reconciliation evidence';
 END IF;
END $$;
DROP TABLE account_deletion_reconciliations;
DROP FUNCTION guard_account_deletion_reconciliation();
DROP VIEW account_deletion_reconciliation_candidates;
DROP VIEW account_deletion_reconciliation_policy;
DROP INDEX account_deletion_reconciliation_scan;
