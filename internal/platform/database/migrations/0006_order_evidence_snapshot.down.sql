ALTER TABLE orders
  DROP CONSTRAINT IF EXISTS orders_refund_window_snapshot_range,
  DROP COLUMN IF EXISTS refund_window_days_snapshot,
  DROP COLUMN IF EXISTS license_name_snapshot,
  DROP COLUMN IF EXISTS product_title_snapshot;
