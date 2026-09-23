DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM payment_intents WHERE compensation_reason IS NOT NULL) THEN
    RAISE EXCEPTION 'Cannot remove recorded product compensation obligations';
  END IF;
END $$;

DROP INDEX payment_intents_active_product_idx;
CREATE UNIQUE INDEX payment_intents_active_product_idx
  ON payment_intents(payer_id,resource_id)
  WHERE purpose='product' AND status IN (
    'checkout_pending','checkout_open','paid','transfer_pending','transferred','refund_pending','refund_failed'
  );
ALTER TABLE payment_intents DROP CONSTRAINT product_compensation_check;
ALTER TABLE payment_intents DROP COLUMN compensation_reason;
