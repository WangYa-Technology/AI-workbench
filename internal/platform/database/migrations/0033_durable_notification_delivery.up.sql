ALTER TABLE notifications
  ADD COLUMN delivery_status text NOT NULL DEFAULT 'delivered'
    CHECK (delivery_status IN ('queued','delivered','suppressed')),
  ADD COLUMN delivery_error_code text
    CHECK (delivery_error_code IS NULL OR delivery_error_code ~ '^[a-z0-9_]{3,80}$'),
  ADD COLUMN delivered_at timestamptz,
  ADD COLUMN suppressed_at timestamptz;

UPDATE notifications SET delivered_at=created_at;
ALTER TABLE notifications ALTER COLUMN delivery_status SET DEFAULT 'queued';

ALTER TABLE notifications ADD CONSTRAINT notifications_delivery_state_check CHECK (
  (delivery_status='queued' AND delivery_error_code IS NULL AND delivered_at IS NULL AND suppressed_at IS NULL) OR
  (delivery_status='delivered' AND delivery_error_code IS NULL AND delivered_at IS NOT NULL AND suppressed_at IS NULL) OR
  (delivery_status='suppressed' AND delivery_error_code='preference_disabled' AND delivered_at IS NULL AND suppressed_at IS NOT NULL)
);

CREATE INDEX notifications_user_delivery_idx
  ON notifications(user_id,created_at DESC,id DESC);
CREATE INDEX notifications_pending_delivery_idx
  ON notifications(created_at,id) WHERE delivery_status='queued';

CREATE FUNCTION protect_terminal_notification_delivery() RETURNS trigger AS $$
BEGIN
  IF OLD.delivery_status IN ('delivered','suppressed') AND
     (NEW.delivery_status <> OLD.delivery_status OR
      NEW.delivery_error_code IS DISTINCT FROM OLD.delivery_error_code OR
      NEW.delivered_at IS DISTINCT FROM OLD.delivered_at OR
      NEW.suppressed_at IS DISTINCT FROM OLD.suppressed_at) THEN
    RAISE EXCEPTION 'terminal notification delivery evidence is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER notifications_delivery_evidence_guard
BEFORE UPDATE ON notifications
FOR EACH ROW EXECUTE FUNCTION protect_terminal_notification_delivery();
