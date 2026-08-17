DELETE FROM role_permissions WHERE permission_id IN (
  'billing:self','admin:overview','admin:users','admin:content','admin:generations',
  'admin:providers','admin:finance','admin:audit'
);
DELETE FROM permissions WHERE id IN (
  'billing:self','admin:overview','admin:users','admin:content','admin:generations',
  'admin:providers','admin:finance','admin:audit'
);

DROP TABLE IF EXISTS provider_profiles;
DROP TABLE IF EXISTS generation_commands;
DROP TABLE IF EXISTS billing_entries;
DROP TABLE IF EXISTS billing_reservations;
DROP TRIGGER IF EXISTS users_create_local_test_billing_account ON users;
DROP FUNCTION IF EXISTS create_local_test_billing_account();
DROP TABLE IF EXISTS billing_accounts;
DROP INDEX IF EXISTS generations_retry_idx;
ALTER TABLE generations
  DROP COLUMN IF EXISTS cancel_reason,
  DROP COLUMN IF EXISTS cancelled_at,
  DROP COLUMN IF EXISTS retry_of_generation_id;
