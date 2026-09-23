DROP INDEX IF EXISTS payment_destinations_identity_idx;
ALTER TABLE payment_destinations DROP CONSTRAINT IF EXISTS payment_destinations_identity_binding_check;
ALTER TABLE payment_destinations
  DROP COLUMN IF EXISTS original_request_version,
  DROP COLUMN IF EXISTS original_api_version,
  DROP COLUMN IF EXISTS original_endpoint,
  DROP COLUMN IF EXISTS original_live_mode,
  DROP COLUMN IF EXISTS original_store_id,
  DROP COLUMN IF EXISTS original_merchant_id;
