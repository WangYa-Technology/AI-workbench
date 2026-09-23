DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM product_settlements)
     OR EXISTS (SELECT 1 FROM product_settlement_events)
     OR EXISTS (SELECT 1 FROM product_payout_batch_items)
     OR EXISTS (SELECT 1 FROM product_payout_batches) THEN
    RAISE EXCEPTION 'cannot remove product seller settlement evidence';
  END IF;
END $$;
DROP TRIGGER product_settlement_events_immutable ON product_settlement_events;
DROP FUNCTION reject_product_settlement_mutation();
DROP TABLE product_settlement_events;
DROP TABLE product_settlement_dispatches;
DROP TABLE product_payout_batch_items;
DROP TABLE product_settlements;
DROP TABLE product_payout_batches;
DROP TABLE product_settlement_settings;
