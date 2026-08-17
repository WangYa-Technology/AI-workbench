ALTER TABLE orders
  ADD COLUMN product_title_snapshot text,
  ADD COLUMN license_name_snapshot text,
  ADD COLUMN refund_window_days_snapshot integer;

UPDATE orders o
SET product_title_snapshot=p.title,
    license_name_snapshot=l.name,
    refund_window_days_snapshot=l.refund_window_days
FROM products p
JOIN licenses l ON l.code=p.license_code
WHERE p.id=o.product_id;

ALTER TABLE orders
  ALTER COLUMN product_title_snapshot SET NOT NULL,
  ALTER COLUMN license_name_snapshot SET NOT NULL,
  ALTER COLUMN refund_window_days_snapshot SET NOT NULL,
  ADD CONSTRAINT orders_refund_window_snapshot_range CHECK (refund_window_days_snapshot BETWEEN 0 AND 90);
