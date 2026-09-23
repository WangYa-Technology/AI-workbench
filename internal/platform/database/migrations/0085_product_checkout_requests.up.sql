-- Freeze the first outbound command before contacting the checkout API.
-- Existing intents cannot be backfilled from mutable users/provider settings:
-- those values need not match the request that may already have reached them.
CREATE TABLE product_checkout_requests (
 payment_id uuid PRIMARY KEY REFERENCES payment_intents(id),
 identity jsonb NOT NULL CHECK (jsonb_typeof(identity)='object'),
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((request->>'PaymentID'=payment_id::text AND request->>'Purpose'='product'
   AND identity->>'provider' IN ('stripe','waffo_pancake')
   AND length(identity->>'merchantId')>0 AND length(identity->>'requestVersion')>0) IS TRUE)
);
CREATE TRIGGER product_checkout_requests_immutable BEFORE UPDATE OR DELETE ON product_checkout_requests
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
