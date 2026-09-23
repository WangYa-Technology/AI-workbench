DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_checkout_dispatches) OR EXISTS(SELECT 1 FROM product_checkout_closures)
 OR EXISTS(SELECT 1 FROM product_checkout_requests WHERE dispatch_protocol='guarded_v1') THEN
  RAISE EXCEPTION 'cannot discard guarded checkout dispatch or closure evidence';
 END IF;
END $$;
DROP VIEW product_checkout_locally_closable;
DROP TABLE product_checkout_closures;
DROP TABLE product_checkout_dispatches;
ALTER TABLE product_checkout_requests DROP COLUMN dispatch_protocol;
