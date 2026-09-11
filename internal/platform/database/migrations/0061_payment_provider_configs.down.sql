DROP TABLE IF EXISTS payment_provider_configs;

ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check CHECK (provider IN ('stripe'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_checkout_url_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_checkout_url_check CHECK (checkout_url IS NULL OR (char_length(checkout_url) BETWEEN 16 AND 2048 AND checkout_url LIKE 'https://checkout.stripe.com/%'));
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_provider_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_provider_check CHECK (provider IN ('stripe'));
