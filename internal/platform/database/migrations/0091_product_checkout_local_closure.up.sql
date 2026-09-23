-- Absence of a dispatch record proves nothing about older deployments.
ALTER TABLE product_checkout_requests ADD COLUMN dispatch_protocol text NOT NULL DEFAULT 'legacy'
 CHECK (dispatch_protocol IN ('legacy','guarded_v1'));

CREATE TABLE product_checkout_dispatches (
 payment_id uuid PRIMARY KEY REFERENCES product_checkout_requests(payment_id),
 request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
 reserved_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER product_checkout_dispatches_immutable BEFORE UPDATE OR DELETE ON product_checkout_dispatches
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE TABLE product_checkout_closures (
 payment_id uuid PRIMARY KEY REFERENCES payment_intents(id),
 order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
 buyer_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 128),
 observed_version bigint NOT NULL CHECK (observed_version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(buyer_id,idempotency_key)
);
CREATE TRIGGER product_checkout_closures_immutable BEFORE UPDATE OR DELETE ON product_checkout_closures
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE VIEW product_checkout_locally_closable AS
 SELECT p.id AS payment_id,o.id AS order_id,o.buyer_id,p.version AS payment_version
 FROM payment_intents p JOIN orders o ON o.id=p.order_id
 JOIN product_checkout_requests r ON r.payment_id=p.id AND r.dispatch_protocol='guarded_v1'
 JOIN product_delivery_snapshots d ON d.order_id=o.id AND d.state IN ('prepared','ready')
 JOIN users buyer ON buyer.id=o.buyer_id AND buyer.status='active'
 WHERE p.purpose='product' AND o.delivery_snapshot_required
 AND p.payer_id=o.buyer_id AND p.resource_id=o.product_id
 AND p.status='checkout_pending' AND o.status='payment_pending'
 AND p.provider_checkout_id IS NULL AND p.provider_payment_id IS NULL AND p.provider_charge_id IS NULL
 AND p.checkout_url IS NULL AND p.checkout_expires_at IS NULL AND p.compensation_reason IS NULL
 AND NOT EXISTS(SELECT 1 FROM product_checkout_dispatches s WHERE s.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_closures c WHERE c.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM payment_provider_events e WHERE e.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM entitlements e WHERE e.order_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_review review WHERE review.payment_id=p.id);
