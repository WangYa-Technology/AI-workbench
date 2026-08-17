DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM developer_webhook_deliveries WHERE secret_revision_id IS NULL)
     OR EXISTS (SELECT 1 FROM developer_webhook_endpoints WHERE personal_data_redacted_at IS NOT NULL) THEN
    RAISE EXCEPTION '0028 cannot be rolled back after Webhook data-rights erasure has executed';
  END IF;
END;
$$;

ALTER TABLE developer_webhook_deliveries
  DROP CONSTRAINT developer_webhook_deliveries_secret_revision_id_fkey;
ALTER TABLE developer_webhook_deliveries
  ALTER COLUMN secret_revision_id SET NOT NULL;
ALTER TABLE developer_webhook_deliveries
  ADD CONSTRAINT developer_webhook_deliveries_secret_revision_id_fkey
  FOREIGN KEY (secret_revision_id) REFERENCES developer_webhook_secret_revisions(id);

ALTER TABLE developer_webhook_endpoints
  DROP COLUMN personal_data_redacted_at;

CREATE OR REPLACE FUNCTION reject_webhook_evidence_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'webhook evidence is immutable';
END;
$$ LANGUAGE plpgsql;
