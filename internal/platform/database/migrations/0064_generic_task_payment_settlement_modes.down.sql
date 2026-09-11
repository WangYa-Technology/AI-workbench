DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM task_settlements
    WHERE mode IN ('provider_pending', 'provider_transferred')
  ) THEN
    RAISE EXCEPTION '0064 cannot be rolled back after generic provider settlement evidence exists';
  END IF;
END;
$$;

ALTER TABLE task_settlements DROP CONSTRAINT task_settlements_mode_check;
ALTER TABLE task_settlements ADD CONSTRAINT task_settlements_mode_check
  CHECK (mode IN ('local_test', 'stripe_pending', 'stripe_transferred'));
