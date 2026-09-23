-- Freeze return URLs with the intent, so retries use the same remote parameters.
ALTER TABLE payment_intents ADD COLUMN product_success_url text;
ALTER TABLE payment_intents ADD COLUMN product_cancel_url text;
ALTER TABLE payment_intents ADD CONSTRAINT product_return_urls_check CHECK (
 (product_success_url IS NULL AND product_cancel_url IS NULL) OR
 (purpose='product' AND product_success_url IS NOT NULL AND product_cancel_url IS NOT NULL)
);
