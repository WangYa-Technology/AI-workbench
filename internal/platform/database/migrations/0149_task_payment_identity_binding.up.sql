-- Task funding is a financial command. Preserve the authenticated Provider
-- identity that created the checkout so a later transfer cannot silently use
-- a different merchant, environment, endpoint, or request contract.
ALTER TABLE payment_intents
  ADD COLUMN task_original_merchant_id text,
  ADD COLUMN task_original_store_id text,
  ADD COLUMN task_original_live_mode boolean,
  ADD COLUMN task_original_endpoint text,
  ADD COLUMN task_original_api_version text,
  ADD COLUMN task_original_request_version text;

ALTER TABLE payment_intents
  ADD CONSTRAINT payment_intents_task_identity_binding_check CHECK (
    (task_original_merchant_id IS NULL AND task_original_store_id IS NULL
      AND task_original_live_mode IS NULL AND task_original_endpoint IS NULL
      AND task_original_api_version IS NULL AND task_original_request_version IS NULL)
    OR (purpose='task' AND length(task_original_merchant_id) > 0
      AND (provider <> 'stripe' OR task_original_merchant_id ~ '^acct_[A-Za-z0-9_]+$')
      AND task_original_live_mode IS NOT NULL AND length(task_original_endpoint) > 0
      AND length(task_original_api_version) > 0 AND length(task_original_request_version) > 0)
  );

ALTER TABLE payment_intents DROP CONSTRAINT payment_intents_status_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_status_check CHECK (status IN (
  'checkout_pending','checkout_open','paid','payment_failed','transfer_pending','transferred',
  'refund_pending','refund_failed','refunded','cancelled','recovery_required'
));

CREATE FUNCTION protect_task_payment_identity() RETURNS trigger AS $$
BEGIN
  IF OLD.task_original_merchant_id IS NOT NULL AND
     ROW(NEW.task_original_merchant_id,NEW.task_original_store_id,NEW.task_original_live_mode,
         NEW.task_original_endpoint,NEW.task_original_api_version,NEW.task_original_request_version)
       IS DISTINCT FROM
     ROW(OLD.task_original_merchant_id,OLD.task_original_store_id,OLD.task_original_live_mode,
         OLD.task_original_endpoint,OLD.task_original_api_version,OLD.task_original_request_version)
  THEN
    RAISE EXCEPTION 'task payment identity evidence is immutable';
  END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER payment_intents_task_identity_guard
BEFORE UPDATE ON payment_intents
FOR EACH ROW EXECUTE FUNCTION protect_task_payment_identity();

CREATE INDEX payment_intents_task_identity_idx
  ON payment_intents(provider, task_original_merchant_id, task_original_live_mode)
  WHERE purpose='task' AND task_original_merchant_id IS NOT NULL;
