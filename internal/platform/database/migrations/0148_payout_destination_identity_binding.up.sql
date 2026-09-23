-- Keep the authenticated merchant/environment that created a connected
-- account beside the mutable capability projection.  NULL means historical
-- or manually entered evidence whose origin is unknown and therefore cannot
-- authorize an automatic seller transfer.
ALTER TABLE payment_destinations
  ADD COLUMN original_merchant_id text,
  ADD COLUMN original_store_id text,
  ADD COLUMN original_live_mode boolean,
  ADD COLUMN original_endpoint text,
  ADD COLUMN original_api_version text,
  ADD COLUMN original_request_version text;

ALTER TABLE payment_destinations
  ADD CONSTRAINT payment_destinations_identity_binding_check CHECK (
    (original_merchant_id IS NULL AND original_live_mode IS NULL AND original_endpoint IS NULL
      AND original_api_version IS NULL AND original_request_version IS NULL)
    OR (original_merchant_id ~ '^acct_[A-Za-z0-9_]+$' AND original_live_mode IS NOT NULL
      AND length(original_endpoint) > 0 AND length(original_api_version) > 0
      AND length(original_request_version) > 0)
  );

CREATE INDEX payment_destinations_identity_idx
  ON payment_destinations(provider, original_merchant_id, original_live_mode)
  WHERE original_merchant_id IS NOT NULL;
