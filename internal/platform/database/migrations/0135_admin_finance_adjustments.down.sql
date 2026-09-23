LOCK TABLE admin_finance_adjustments IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM admin_finance_adjustments) THEN
  RAISE EXCEPTION 'cannot discard finance adjustment replay evidence';
 END IF;
END $$;
DROP TRIGGER finance_adjustment_entry_guard ON billing_entries;
DROP FUNCTION check_finance_adjustment_entry();
DROP TABLE admin_finance_adjustments;
DROP FUNCTION check_finance_adjustment_receipt();
DROP FUNCTION protect_finance_adjustment_command();
