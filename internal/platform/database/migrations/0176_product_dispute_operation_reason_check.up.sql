ALTER TABLE product_payment_dispute_operations
  DROP CONSTRAINT IF EXISTS product_payment_dispute_operations_reason_check;

ALTER TABLE product_payment_dispute_operations
  ADD CONSTRAINT product_payment_dispute_operations_reason_check
  CHECK (char_length(reason) BETWEEN 10 AND 2000);
