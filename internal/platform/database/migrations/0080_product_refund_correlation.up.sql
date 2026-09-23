-- Retain only the signed correlation identifier, never the raw provider payload.
ALTER TABLE payment_provider_events ADD COLUMN refund_operation_id uuid;
-- Do not change the parameters of a refund already sent with an idempotency key.
-- New operations explicitly opt in; existing/retried operations keep their form.
ALTER TABLE orders ADD COLUMN refund_correlation_enabled boolean NOT NULL DEFAULT false;
