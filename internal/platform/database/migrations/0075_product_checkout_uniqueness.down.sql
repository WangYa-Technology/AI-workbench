DROP TABLE product_checkout_commands;
DROP INDEX payment_intents_active_product_idx;
-- Rollback deliberately fails when different buyers have reused a key. Resolve
-- historical command evidence explicitly rather than deleting order records.
ALTER TABLE orders DROP CONSTRAINT orders_buyer_idempotency_key;
ALTER TABLE orders ADD CONSTRAINT orders_idempotency_key_key UNIQUE(idempotency_key);
