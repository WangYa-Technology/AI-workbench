ALTER TABLE product_payment_disputes
  DROP CONSTRAINT IF EXISTS product_payment_disputes_latest_event_fk,
  DROP COLUMN IF EXISTS latest_event_id;
