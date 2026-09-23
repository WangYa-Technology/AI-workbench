DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM payment_intents
    WHERE task_original_merchant_id IS NOT NULL OR task_original_store_id IS NOT NULL
       OR task_original_live_mode IS NOT NULL OR task_original_endpoint IS NOT NULL
       OR task_original_api_version IS NOT NULL OR task_original_request_version IS NOT NULL) THEN
    RAISE EXCEPTION 'cannot remove task payment identity evidence after it has been recorded';
  END IF;
END $$;

DROP INDEX IF EXISTS payment_intents_task_identity_idx;
DROP TRIGGER IF EXISTS payment_intents_task_identity_guard ON payment_intents;
DROP FUNCTION IF EXISTS protect_task_payment_identity();
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_task_identity_binding_check;
ALTER TABLE payment_intents
  DROP COLUMN IF EXISTS task_original_request_version,
  DROP COLUMN IF EXISTS task_original_api_version,
  DROP COLUMN IF EXISTS task_original_endpoint,
  DROP COLUMN IF EXISTS task_original_live_mode,
  DROP COLUMN IF EXISTS task_original_store_id,
  DROP COLUMN IF EXISTS task_original_merchant_id;
ALTER TABLE payment_intents DROP CONSTRAINT payment_intents_status_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_status_check CHECK (status IN (
  'checkout_pending','checkout_open','paid','payment_failed','transfer_pending','transferred',
  'refund_pending','refund_failed','refunded','cancelled'
));
