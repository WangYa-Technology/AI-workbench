DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_checkout_requests) THEN
  RAISE EXCEPTION 'cannot discard original product checkout requests';
 END IF;
END $$;
DROP TABLE product_checkout_requests;
