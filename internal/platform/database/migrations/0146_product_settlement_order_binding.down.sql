DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM product_settlements ps
    JOIN payment_intents pi ON pi.id=ps.payment_id
    WHERE pi.order_id IS DISTINCT FROM ps.order_id
  ) THEN
    RAISE EXCEPTION 'cannot downgrade product settlement order binding: mismatch requires reconciliation';
  END IF;
END $$;

ALTER TABLE product_settlements
  DROP CONSTRAINT product_settlements_payment_order_fkey;

ALTER TABLE payment_intents
  DROP CONSTRAINT payment_intents_id_order_unique;
