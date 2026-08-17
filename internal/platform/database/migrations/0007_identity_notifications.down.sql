DROP TABLE IF EXISTS notification_preferences;

DROP INDEX IF EXISTS notifications_user_unread_idx;
DROP INDEX IF EXISTS notifications_user_inbox_idx;
DROP INDEX IF EXISTS notifications_user_source_key_unique;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_target_path_check;
ALTER TABLE notifications
  DROP COLUMN IF EXISTS source_key,
  DROP COLUMN IF EXISTS resource_id,
  DROP COLUMN IF EXISTS resource_type;

DROP TABLE IF EXISTS oauth_accounts;
DROP TABLE IF EXISTS oauth_provider_configs;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;

DROP INDEX IF EXISTS sessions_user_activity_idx;
ALTER TABLE sessions
  DROP COLUMN IF EXISTS last_seen_at,
  DROP COLUMN IF EXISTS network_hash,
  DROP COLUMN IF EXISTS client_label;

UPDATE users SET role='creator' WHERE role='publisher';
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users
  ADD CONSTRAINT users_role_check CHECK (role IN ('member','creator','moderator','admin'));
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
