DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM payment_provider_events WHERE refund_operation_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM orders WHERE refund_correlation_enabled) THEN
  RAISE EXCEPTION 'Cannot discard signed refund operation evidence';
 END IF;
END $$;
ALTER TABLE payment_provider_events DROP COLUMN refund_operation_id;
ALTER TABLE orders DROP COLUMN refund_correlation_enabled;
