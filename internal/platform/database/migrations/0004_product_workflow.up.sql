ALTER TABLE orders
  ADD COLUMN license_version text,
  ADD COLUMN license_terms_snapshot text,
  ADD COLUMN refund_reason text NOT NULL DEFAULT '',
  ADD COLUMN refund_idempotency_key text,
  ADD COLUMN refund_operation_id uuid,
  ADD COLUMN refund_requested_at timestamptz,
  ADD COLUMN refunded_at timestamptz;

ALTER TABLE products
  ADD COLUMN ai_disclosure text NOT NULL DEFAULT 'The seller must disclose AI-assisted and source media used in this product.',
  ADD COLUMN included_files jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN compatibility text NOT NULL DEFAULT '';

UPDATE orders o
SET license_version=l.version,
    license_terms_snapshot=l.terms
FROM products p
JOIN licenses l ON l.code=p.license_code
WHERE p.id=o.product_id;

ALTER TABLE orders
  ALTER COLUMN license_version SET NOT NULL,
  ALTER COLUMN license_terms_snapshot SET NOT NULL;

CREATE UNIQUE INDEX orders_refund_idempotency_idx
  ON orders(buyer_id,refund_idempotency_key) WHERE refund_idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX orders_refund_operation_idx
  ON orders(refund_operation_id) WHERE refund_operation_id IS NOT NULL;
CREATE INDEX products_marketplace_idx
  ON products(status,product_type,created_at DESC);
