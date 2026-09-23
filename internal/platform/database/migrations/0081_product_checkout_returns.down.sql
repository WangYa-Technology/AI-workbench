DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM payment_intents WHERE product_success_url IS NOT NULL) THEN
  RAISE EXCEPTION 'Cannot discard persisted checkout return parameters';
 END IF;
END $$;
ALTER TABLE payment_intents DROP CONSTRAINT product_return_urls_check;
ALTER TABLE payment_intents DROP COLUMN product_success_url;
ALTER TABLE payment_intents DROP COLUMN product_cancel_url;
