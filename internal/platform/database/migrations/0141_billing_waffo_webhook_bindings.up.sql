-- Legacy minimized events do not prove a signed buyer/store/order identity.
-- Only a freshly verified delivery can create a binding; never backfill it.
CREATE TABLE billing_waffo_webhook_bindings (
 event_id uuid PRIMARY KEY REFERENCES payment_provider_events(id),
 payment_id uuid NOT NULL REFERENCES billing_checkout_dispatches(payment_id),
 contract_version text NOT NULL CHECK(contract_version='waffo-webhook-v1'),
 original_merchant_id text NOT NULL CHECK(length(original_merchant_id)>0),
 store_id text NOT NULL CHECK(length(store_id)>0),
 live_mode boolean NOT NULL,
 order_external_id uuid NOT NULL CHECK(order_external_id=payment_id),
 buyer_id uuid NOT NULL REFERENCES users(id),
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER billing_waffo_webhook_bindings_immutable BEFORE UPDATE OR DELETE ON billing_waffo_webhook_bindings
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
