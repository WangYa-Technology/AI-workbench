-- Never infer an original request or dispatch permit for legacy intents.
CREATE TABLE billing_checkout_requests (
 payment_id uuid PRIMARY KEY REFERENCES payment_intents(id),
 identity jsonb NOT NULL CHECK (jsonb_typeof(identity)='object'),
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((request->>'PaymentID'=payment_id::text
   AND request->>'Purpose' IN ('wallet_topup','subscription')
   AND identity->>'provider' IN ('stripe','waffo_pancake')
   AND length(identity->>'merchantId')>0 AND length(identity->>'requestVersion')>0) IS TRUE)
);
CREATE TRIGGER billing_checkout_requests_immutable BEFORE UPDATE OR DELETE ON billing_checkout_requests
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE TABLE billing_checkout_dispatches (
 payment_id uuid PRIMARY KEY REFERENCES billing_checkout_requests(payment_id),
 request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
 payment_version bigint NOT NULL CHECK (payment_version>0),
 reserved_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER billing_checkout_dispatches_immutable BEFORE UPDATE OR DELETE ON billing_checkout_dispatches
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();
