ALTER TABLE payment_intents ADD COLUMN compensation_reason text;
ALTER TABLE payment_intents ADD CONSTRAINT product_compensation_check CHECK (
  compensation_reason IS NULL OR (
    purpose='product' AND compensation_reason IN (
      'source_unavailable','buyer_unavailable','already_owned','contract_unavailable','checkout_closed'
    ) AND status IN ('refund_pending','refund_failed','refunded')
  )
);

-- A closed historical checkout may be paid after another order was fulfilled.
-- Its refund obligation must not compete with the legitimate active purchase.
DROP INDEX payment_intents_active_product_idx;
CREATE UNIQUE INDEX payment_intents_active_product_idx
  ON payment_intents(payer_id,resource_id)
  WHERE purpose='product' AND compensation_reason IS NULL AND status IN (
    'checkout_pending','checkout_open','paid','transfer_pending','transferred','refund_pending','refund_failed'
  );
