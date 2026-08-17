ALTER TABLE order_events
  DROP CONSTRAINT IF EXISTS order_events_order_sequence_unique,
  DROP CONSTRAINT IF EXISTS order_events_positive_sequence,
  DROP COLUMN IF EXISTS sequence;
