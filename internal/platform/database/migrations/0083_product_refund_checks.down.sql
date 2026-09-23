DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_refund_checks) THEN
  RAISE EXCEPTION 'cannot discard refund query evidence';
 END IF;
END $$;
DROP VIEW product_refund_review;
ALTER TABLE payment_provider_events DROP CONSTRAINT payment_refund_query_evidence;
ALTER TABLE payment_provider_events DROP COLUMN refund_check_id, DROP COLUMN evidence_source;
DROP TABLE product_refund_checks;
DROP FUNCTION protect_refund_check_evidence();
