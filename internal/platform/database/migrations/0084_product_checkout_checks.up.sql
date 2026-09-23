ALTER TABLE payment_provider_events ADD COLUMN checkout_job_id uuid REFERENCES jobs(id);
ALTER TABLE payment_provider_events DROP CONSTRAINT payment_refund_query_evidence;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_query_evidence CHECK ((
 (evidence_source='webhook' AND refund_check_id IS NULL AND checkout_job_id IS NULL) OR
 (evidence_source='provider_query' AND provider='stripe' AND purpose='product' AND (
   (event_type='refund.observed' AND refund_check_id IS NOT NULL AND checkout_job_id IS NULL) OR
   (event_type='checkout.observed' AND checkout_job_id IS NOT NULL AND refund_check_id IS NULL
     AND payment_status='paid' AND provider_payment_id IS NOT NULL AND provider_charge_id IS NOT NULL)
 ))
) IS TRUE);
CREATE UNIQUE INDEX product_checkout_check_active ON jobs ((payload->>'paymentId'))
 WHERE kind='payment.check_product_checkout' AND status IN ('queued','running');

-- Recover previously opened sessions without declaring local expiry a terminal
-- payment result. The worker must authenticate and verify the provider state.
INSERT INTO jobs(kind,payload,max_attempts,available_at)
 SELECT 'payment.check_product_checkout',jsonb_build_object('paymentId',id::text),20,
 GREATEST(now(),checkout_expires_at+interval '5 seconds')
 FROM payment_intents WHERE purpose='product' AND provider='stripe' AND status='checkout_open'
 AND provider_checkout_id IS NOT NULL AND checkout_expires_at IS NOT NULL;
