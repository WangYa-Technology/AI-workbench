ALTER TABLE task_settlements DROP CONSTRAINT task_settlements_mode_check;
ALTER TABLE task_settlements ADD CONSTRAINT task_settlements_mode_check
  CHECK (mode IN (
    'local_test',
    'stripe_pending',
    'stripe_transferred',
    'provider_pending',
    'provider_transferred'
  ));
