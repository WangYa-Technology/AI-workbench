DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM payment_provider_events WHERE checkout_job_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM jobs WHERE kind='payment.check_product_checkout') THEN
  RAISE EXCEPTION 'cannot discard checkout query evidence or scheduled checks';
 END IF;
END $$;
DROP INDEX product_checkout_check_active;
ALTER TABLE payment_provider_events DROP CONSTRAINT payment_query_evidence;
ALTER TABLE payment_provider_events DROP COLUMN checkout_job_id;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_refund_query_evidence CHECK (
 (evidence_source='webhook' AND refund_check_id IS NULL) OR
 (evidence_source='provider_query' AND refund_check_id IS NOT NULL AND provider='stripe' AND event_type='refund.observed' AND purpose='product')
);
