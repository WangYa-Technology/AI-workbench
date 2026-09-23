LOCK TABLE product_webhook_quarantines IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_webhook_quarantines) THEN
  RAISE EXCEPTION 'signed rejected payment evidence exists; rollback refused';
 END IF;
END $$;
CREATE OR REPLACE VIEW product_delivery_cleanup_policy AS
 SELECT d.order_id,d.state,
 (EXISTS(SELECT 1 FROM payment_intents p WHERE p.order_id=d.order_id AND p.status IN ('checkout_pending','checkout_open')) OR
  EXISTS(SELECT 1 FROM entitlements e JOIN users u ON u.id=e.user_id WHERE e.order_id=d.order_id AND e.status='active' AND u.status<>'deleted')) AS needed,
 EXISTS(SELECT 1 FROM data_rights_legal_holds h JOIN product_delivery_cleanup_subjects s ON s.user_id=h.user_id
  WHERE s.order_id=d.order_id AND h.status='active' AND h.expires_at>now()) AS held
 FROM product_delivery_snapshots d;
CREATE OR REPLACE VIEW product_refund_review AS
 SELECT payment_id FROM product_refund_funds_review
 UNION SELECT payment_id FROM product_refund_dispatch_review;
DROP VIEW product_webhook_quarantine_review;
DROP VIEW product_webhook_quarantine_matches;
DROP TABLE product_webhook_quarantine_checks;
DROP TABLE product_webhook_quarantines;
DROP FUNCTION protect_product_webhook_quarantine();
