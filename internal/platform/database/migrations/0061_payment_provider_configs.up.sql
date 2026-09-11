ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check CHECK (provider IN ('stripe','waffo_pancake','epay'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_checkout_url_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_checkout_url_check CHECK (checkout_url IS NULL OR (char_length(checkout_url) BETWEEN 16 AND 2048 AND checkout_url ~ '^https://'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_checkout_id_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_checkout_id_check CHECK (provider_checkout_id IS NULL OR (char_length(provider_checkout_id) BETWEEN 6 AND 255 AND provider_checkout_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_payment_id_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_payment_id_check CHECK (provider_payment_id IS NULL OR (char_length(provider_payment_id) BETWEEN 6 AND 255 AND provider_payment_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_charge_id_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_charge_id_check CHECK (provider_charge_id IS NULL OR (char_length(provider_charge_id) BETWEEN 6 AND 255 AND provider_charge_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_refund_id_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_refund_id_check CHECK (provider_refund_id IS NULL OR (char_length(provider_refund_id) BETWEEN 6 AND 255 AND provider_refund_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_transfer_id_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_transfer_id_check CHECK (provider_transfer_id IS NULL OR (char_length(provider_transfer_id) BETWEEN 6 AND 255 AND provider_transfer_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_destination_id_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_destination_id_check CHECK (destination_id IS NULL OR (char_length(destination_id) BETWEEN 6 AND 255 AND destination_id ~ '^[A-Za-z0-9_-]+$'));

ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_provider_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_provider_check CHECK (provider IN ('stripe','waffo_pancake','epay'));
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_provider_event_id_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_provider_event_id_check CHECK (char_length(provider_event_id) BETWEEN 8 AND 255 AND provider_event_id ~ '^[A-Za-z0-9_-]+$');
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_object_id_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_object_id_check CHECK (char_length(object_id) BETWEEN 6 AND 255 AND object_id ~ '^[A-Za-z0-9_-]+$');
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_provider_payment_id_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_provider_payment_id_check CHECK (provider_payment_id IS NULL OR (char_length(provider_payment_id) BETWEEN 6 AND 255 AND provider_payment_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_provider_charge_id_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_provider_charge_id_check CHECK (provider_charge_id IS NULL OR (char_length(provider_charge_id) BETWEEN 6 AND 255 AND provider_charge_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_provider_transfer_id_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_provider_transfer_id_check CHECK (provider_transfer_id IS NULL OR (char_length(provider_transfer_id) BETWEEN 6 AND 255 AND provider_transfer_id ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_destination_id_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_destination_id_check CHECK (destination_id IS NULL OR (char_length(destination_id) BETWEEN 6 AND 255 AND destination_id ~ '^[A-Za-z0-9_-]+$'));

CREATE TABLE payment_provider_configs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL CHECK (provider IN ('stripe','waffo_pancake','epay')),
  enabled boolean NOT NULL DEFAULT false,
  environment text NOT NULL CHECK (environment IN ('test','prod')),
  merchant_id text NOT NULL DEFAULT '' CHECK (char_length(merchant_id) <= 255),
  store_id text NOT NULL DEFAULT '' CHECK (char_length(store_id) <= 255),
  product_id_onetime text NOT NULL DEFAULT '' CHECK (char_length(product_id_onetime) <= 255),
  product_id_subscription text NOT NULL DEFAULT '' CHECK (char_length(product_id_subscription) <= 255),
  secret_configured boolean NOT NULL DEFAULT false,
  connector_configured boolean NOT NULL DEFAULT false,
  created_by uuid REFERENCES users(id),
  updated_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider)
);
CREATE INDEX payment_provider_configs_enabled_idx ON payment_provider_configs(enabled,provider);
