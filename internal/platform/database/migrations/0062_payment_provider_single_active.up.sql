-- Product checkout has one active Provider at a time. Other providers remain
-- configured and can be enabled atomically by the Admin update transaction.
CREATE UNIQUE INDEX payment_provider_configs_single_enabled_idx
  ON payment_provider_configs ((enabled)) WHERE enabled;
