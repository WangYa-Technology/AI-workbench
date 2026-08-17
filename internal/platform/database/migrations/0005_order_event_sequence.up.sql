ALTER TABLE order_events ADD COLUMN sequence integer;

WITH ranked AS (
  SELECT id,row_number() OVER (PARTITION BY order_id ORDER BY created_at,id)::integer AS sequence
  FROM order_events
)
UPDATE order_events e SET sequence=ranked.sequence FROM ranked WHERE ranked.id=e.id;

ALTER TABLE order_events
  ALTER COLUMN sequence SET NOT NULL,
  ADD CONSTRAINT order_events_positive_sequence CHECK (sequence > 0),
  ADD CONSTRAINT order_events_order_sequence_unique UNIQUE(order_id,sequence);
