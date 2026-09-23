DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_refund_dispatches)
    OR EXISTS(SELECT 1 FROM product_refund_dispatch_review) THEN
  RAISE EXCEPTION 'cannot discard product refund dispatch provenance';
 END IF;
END $$;
CREATE OR REPLACE VIEW product_refund_review AS
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

DROP VIEW product_refund_funds_review;
DROP VIEW product_refund_dispatch_review;
DROP TABLE product_refund_dispatches;
DROP FUNCTION protect_product_refund_dispatch();
