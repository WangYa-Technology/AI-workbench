ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_target_path_check;

ALTER TABLE notifications
  ADD CONSTRAINT notifications_target_path_check
  CHECK (target_path ~ '^/[A-Za-z0-9/_?=&.%:-]*$' AND target_path !~ '^//');
