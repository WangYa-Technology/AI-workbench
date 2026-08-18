DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM payment_intents) OR EXISTS (SELECT 1 FROM payment_destinations) OR
     EXISTS (SELECT 1 FROM task_settlements WHERE mode<>'local_test') THEN
    RAISE EXCEPTION '0038 cannot be rolled back after payment workflow evidence exists';
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS payment_intent_events_immutable ON payment_intent_events;
DROP TABLE IF EXISTS payment_intent_events;
DROP TABLE IF EXISTS payment_destinations;
DROP TABLE IF EXISTS payment_intents;

ALTER TABLE task_settlements DROP CONSTRAINT task_settlements_mode_check;
ALTER TABLE task_settlements ADD CONSTRAINT task_settlements_mode_check CHECK (mode='local_test');

ALTER TABLE order_events DROP CONSTRAINT order_events_to_status_check;
ALTER TABLE order_events ADD CONSTRAINT order_events_to_status_check CHECK (to_status IN ('test_pending','test_paid','fulfilled','refund_requested','test_refunded','cancelled'));
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN ('test_pending','test_paid','fulfilled','refund_requested','test_refunded','cancelled'));
