-- Only new operations created by the coordinated release receive a permit.
-- Absence on an old operation is uncertainty, not proof it was never sent.
CREATE TABLE product_refund_dispatches (
 operation_id uuid PRIMARY KEY REFERENCES product_refund_attempts(operation_id),
 contract_version text NOT NULL CHECK(contract_version='waffo-product-refund-v1'),
 created_at timestamptz NOT NULL DEFAULT now(),
 reserved_at timestamptz,
 responded_at timestamptz,
 provider_refund_id text,
 CHECK(responded_at IS NULL OR reserved_at IS NOT NULL),
 CHECK((responded_at IS NULL)=(provider_refund_id IS NULL)),
 CHECK(provider_refund_id IS NULL OR length(provider_refund_id)>0)
);
CREATE FUNCTION protect_product_refund_dispatch() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' OR
   ROW(NEW.operation_id,NEW.contract_version,NEW.created_at) IS DISTINCT FROM
   ROW(OLD.operation_id,OLD.contract_version,OLD.created_at) OR
   (OLD.reserved_at IS NOT NULL AND NEW.reserved_at IS DISTINCT FROM OLD.reserved_at) OR
   (OLD.responded_at IS NOT NULL AND ROW(NEW.responded_at,NEW.provider_refund_id) IS DISTINCT FROM
     ROW(OLD.responded_at,OLD.provider_refund_id)) THEN
  RAISE EXCEPTION 'product refund dispatch evidence is immutable';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_refund_dispatch_evidence BEFORE UPDATE OR DELETE ON product_refund_dispatches
 FOR EACH ROW EXECUTE FUNCTION protect_product_refund_dispatch();

CREATE VIEW product_refund_dispatch_review AS
 SELECT a.payment_id,a.operation_id FROM product_refund_attempts a
 LEFT JOIN product_refund_dispatches d ON d.operation_id=a.operation_id
 WHERE a.provider='waffo_pancake' AND a.status IN ('requested','pending')
 AND (d.operation_id IS NULL OR (d.reserved_at IS NOT NULL AND d.responded_at IS NULL));

CREATE VIEW product_refund_funds_review AS
SELECT pi.id AS payment_id FROM payment_intents pi
WHERE pi.purpose='product' AND (
 EXISTS(SELECT 1 FROM product_checkout_lookup_review l WHERE l.payment_id=pi.id)
 OR EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
 OR EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=pi.id AND a.reconciliation_required)
 OR COALESCE((SELECT c.status IN ('requested','observed') OR c.unresolved_count>0
   OR (c.status='failed' AND c.observed_at IS NOT NULL)
   FROM product_refund_checks c WHERE c.payment_id=pi.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false)
 -- Recovery establishes merchant ownership, not the absence of past refunds.
 -- Require a completed funds check after recovery, including after failures
 -- with no observation. Unknown/manual refunds continue to hold this gate.
 OR EXISTS(SELECT 1 FROM product_payment_identity_recoveries r WHERE r.payment_id=pi.id
   AND pi.provider_payment_id IS NOT NULL AND pi.status IN ('paid','refund_pending','refund_failed','refunded')
   AND NOT COALESCE((SELECT c.status='completed' AND c.unresolved_count=0 AND c.observed_at>=r.created_at
     FROM product_refund_checks c WHERE c.payment_id=pi.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false))
);

CREATE OR REPLACE VIEW product_refund_review AS
 SELECT payment_id FROM product_refund_funds_review
 UNION SELECT payment_id FROM product_refund_dispatch_review;
