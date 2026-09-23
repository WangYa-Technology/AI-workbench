-- No backfill: past minimized events do not prove which store/customer/order
-- the signature covered. Only newly verified raw deliveries can add evidence.
CREATE TABLE product_waffo_webhook_bindings (
 event_id uuid PRIMARY KEY REFERENCES payment_provider_events(id),
 payment_id uuid NOT NULL REFERENCES payment_intents(id),
 contract_version text NOT NULL CHECK(contract_version='waffo-webhook-v1'),
 original_merchant_id text NOT NULL CHECK(length(original_merchant_id)>0),
 store_id text NOT NULL CHECK(length(store_id)>0),
 live_mode boolean NOT NULL,
 order_id uuid NOT NULL REFERENCES orders(id),
 buyer_id uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX product_waffo_webhook_payment ON product_waffo_webhook_bindings(payment_id);
CREATE TRIGGER product_waffo_webhook_binding_immutable BEFORE UPDATE OR DELETE ON product_waffo_webhook_bindings
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
