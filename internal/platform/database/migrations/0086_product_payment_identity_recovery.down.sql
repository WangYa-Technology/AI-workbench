DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_payment_identity_recoveries)
   OR EXISTS(SELECT 1 FROM jobs WHERE kind='payment.verify_product_identity') THEN
   RAISE EXCEPTION 'cannot discard product merchant recovery evidence or jobs';
 END IF;
END $$;
CREATE OR REPLACE VIEW product_refund_review AS
SELECT pi.id AS payment_id FROM payment_intents pi
WHERE pi.purpose='product' AND (
 EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=pi.id AND a.reconciliation_required)
 OR COALESCE((SELECT c.status IN ('requested','observed') OR c.unresolved_count>0
   OR (c.status='failed' AND c.observed_at IS NOT NULL)
   FROM product_refund_checks c WHERE c.payment_id=pi.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false)
);
DROP VIEW product_payment_identity_gaps;
DROP INDEX product_payment_identity_recovery_active;
DROP TABLE product_payment_identity_recoveries;
