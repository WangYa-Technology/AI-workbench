DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM jobs WHERE kind='notification.deliver') OR
     EXISTS (SELECT 1 FROM notifications WHERE delivery_status <> 'delivered') THEN
    RAISE EXCEPTION '0033 cannot be rolled back after durable notification delivery evidence exists';
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS notifications_delivery_evidence_guard ON notifications;
DROP FUNCTION IF EXISTS protect_terminal_notification_delivery();
DROP INDEX IF EXISTS notifications_pending_delivery_idx;
DROP INDEX IF EXISTS notifications_user_delivery_idx;
ALTER TABLE notifications DROP CONSTRAINT notifications_delivery_state_check;
ALTER TABLE notifications
  DROP COLUMN suppressed_at,
  DROP COLUMN delivered_at,
  DROP COLUMN delivery_error_code,
  DROP COLUMN delivery_status;
