DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_refund_read_receipts) THEN
  RAISE EXCEPTION 'cannot discard late refund read receipts';
 END IF;
END $$;

CREATE OR REPLACE VIEW product_refund_observation_gaps AS
 SELECT c.payment_id,c.id AS check_id,observation->>'providerId' AS provider_refund_id
 FROM product_refund_checks c
 CROSS JOIN LATERAL jsonb_array_elements(COALESCE(c.observations,'[]'::jsonb)) observation
 WHERE c.observed_at IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=c.payment_id AND a.provider='stripe'
   AND a.provider_refund_id=observation->>'providerId'
   AND a.provider_payment_id=observation->>'providerPaymentId'
   AND a.amount_cents::text=observation->>'amountCents' AND a.currency=observation->>'currency'
   AND (observation->>'operationId' IS NULL OR a.operation_id::text=observation->>'operationId')
   AND (observation->>'status'<>'succeeded' OR a.status='succeeded')
 );
DROP TABLE product_refund_read_receipts;
DROP FUNCTION protect_refund_read_receipt();
