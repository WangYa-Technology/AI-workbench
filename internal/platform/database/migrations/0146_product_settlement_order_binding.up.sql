-- A settlement must never combine the order from one payment with the payment
-- from another order. Abort explicitly if legacy data needs reconciliation;
-- do not silently rewrite financial evidence while adding the guard.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM product_settlements ps
    JOIN payment_intents pi ON pi.id=ps.payment_id
    WHERE pi.order_id IS DISTINCT FROM ps.order_id
  ) THEN
    RAISE EXCEPTION 'cannot add product settlement order binding: existing mismatch requires reconciliation';
  END IF;
END $$;

ALTER TABLE payment_intents
  ADD CONSTRAINT payment_intents_id_order_unique UNIQUE (id,order_id);

ALTER TABLE product_settlements
  ADD CONSTRAINT product_settlements_payment_order_fkey
  FOREIGN KEY (payment_id,order_id) REFERENCES payment_intents(id,order_id);
