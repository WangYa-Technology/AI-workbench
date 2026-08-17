DROP INDEX IF EXISTS products_marketplace_idx;
DROP INDEX IF EXISTS orders_refund_operation_idx;
DROP INDEX IF EXISTS orders_refund_idempotency_idx;

ALTER TABLE products
  DROP COLUMN IF EXISTS compatibility,
  DROP COLUMN IF EXISTS included_files,
  DROP COLUMN IF EXISTS ai_disclosure;

ALTER TABLE orders
  DROP COLUMN IF EXISTS refunded_at,
  DROP COLUMN IF EXISTS refund_requested_at,
  DROP COLUMN IF EXISTS refund_operation_id,
  DROP COLUMN IF EXISTS refund_idempotency_key,
  DROP COLUMN IF EXISTS refund_reason,
  DROP COLUMN IF EXISTS license_terms_snapshot,
  DROP COLUMN IF EXISTS license_version;
