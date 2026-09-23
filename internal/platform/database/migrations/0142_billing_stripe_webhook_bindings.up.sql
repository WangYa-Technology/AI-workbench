-- A metadata claim alone cannot establish which Checkout collected funds.
-- Bind only newly verified original-session deliveries; do not infer history.
CREATE TABLE billing_stripe_checkout_bindings (
 payment_id uuid PRIMARY KEY REFERENCES billing_checkout_dispatches(payment_id),
 original_merchant_id text NOT NULL CHECK(length(original_merchant_id)>0),
 live_mode boolean NOT NULL,
 checkout_id text NOT NULL CHECK(checkout_id ~ '^cs_[A-Za-z0-9_]+$'),
 provider_payment_id text NOT NULL CHECK(provider_payment_id ~ '^pi_[A-Za-z0-9_]+$'),
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 anchor_event_id uuid NOT NULL UNIQUE REFERENCES payment_provider_events(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(original_merchant_id,live_mode,checkout_id),
 UNIQUE(original_merchant_id,live_mode,provider_payment_id)
);
CREATE TRIGGER billing_stripe_checkout_bindings_immutable BEFORE UPDATE OR DELETE ON billing_stripe_checkout_bindings
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE TABLE billing_stripe_webhook_bindings (
 event_id uuid PRIMARY KEY REFERENCES payment_provider_events(id),
 payment_id uuid NOT NULL REFERENCES billing_stripe_checkout_bindings(payment_id),
 contract_version text NOT NULL CHECK(contract_version='stripe-billing-webhook-v1'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER billing_stripe_webhook_bindings_immutable BEFORE UPDATE OR DELETE ON billing_stripe_webhook_bindings
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
