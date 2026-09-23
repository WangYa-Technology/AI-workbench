-- Existing duplicate live sessions require provider reconciliation before this
-- migration. Never silently cancel potentially charged historical orders.
ALTER TABLE orders DROP CONSTRAINT orders_idempotency_key_key;
ALTER TABLE orders ADD CONSTRAINT orders_buyer_idempotency_key UNIQUE(buyer_id,idempotency_key);

CREATE UNIQUE INDEX payment_intents_active_product_idx
  ON payment_intents(payer_id,resource_id)
  WHERE purpose='product' AND status IN (
    'checkout_pending','checkout_open','paid','transfer_pending','transferred','refund_pending','refund_failed'
  );

-- Alternate retry keys stay bound to the same intent, including after it closes.
CREATE TABLE product_checkout_commands (
  buyer_id uuid NOT NULL REFERENCES users(id),
  idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 128),
  payment_id uuid NOT NULL REFERENCES payment_intents(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (buyer_id,idempotency_key)
);

INSERT INTO product_checkout_commands(buyer_id,idempotency_key,payment_id,created_at)
SELECT payer_id,idempotency_key,id,created_at FROM payment_intents WHERE purpose='product';
