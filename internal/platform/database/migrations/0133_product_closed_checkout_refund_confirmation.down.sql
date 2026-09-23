DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_closed_checkout_refund_confirmations) THEN
  RAISE EXCEPTION 'cannot remove closed checkout refund confirmation evidence';
 END IF;
END $$;
CREATE OR REPLACE VIEW product_closed_checkout_funds_ready AS
 SELECT r.payment_id,r.event_id FROM product_closed_checkout_recoveries r
 WHERE COALESCE((SELECT c.status='completed' AND c.unresolved_count=0 AND c.observed_at>=r.created_at AND c.created_at>=r.created_at
 AND EXISTS(SELECT 1 FROM product_refund_read_executions x WHERE x.check_id=c.id AND x.complete AND x.started_at>=r.created_at)
 FROM product_refund_checks c WHERE c.payment_id=r.payment_id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_evidence_conflicts g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookups l WHERE l.payment_id=r.payment_id AND l.outcome IN ('ambiguous','state_changed'))
 AND NOT EXISTS(SELECT 1 FROM product_webhook_quarantine_review g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_observation_review g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=r.payment_id AND a.reconciliation_required)
 -- Existing refund obligations and historical rights need their own disposition.
 -- A clean remote read cannot authorize a second compensation for them.
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=r.payment_id AND a.requested_at<=r.created_at)
 AND NOT EXISTS(SELECT 1 FROM payment_intents p JOIN entitlements e ON e.order_id=p.order_id WHERE p.id=r.payment_id);
DROP VIEW product_closed_checkout_refund_candidates;
DROP TABLE product_closed_checkout_refund_confirmations;
DROP FUNCTION validate_product_closed_checkout_refund_confirmation();
