ALTER TABLE developer_webhook_endpoints
  ADD COLUMN personal_data_redacted_at timestamptz;

ALTER TABLE developer_webhook_deliveries
  DROP CONSTRAINT developer_webhook_deliveries_secret_revision_id_fkey;
ALTER TABLE developer_webhook_deliveries
  ALTER COLUMN secret_revision_id DROP NOT NULL;
ALTER TABLE developer_webhook_deliveries
  ADD CONSTRAINT developer_webhook_deliveries_secret_revision_id_fkey
  FOREIGN KEY (secret_revision_id) REFERENCES developer_webhook_secret_revisions(id) ON DELETE SET NULL;

CREATE OR REPLACE FUNCTION reject_webhook_evidence_mutation() RETURNS trigger AS $$
BEGIN
  IF current_setting('app.webhook_data_rights_maintenance', true) = 'on' THEN
    IF TG_TABLE_NAME = 'developer_webhook_secret_revisions' AND TG_OP = 'DELETE' THEN
      RETURN OLD;
    END IF;
    IF TG_TABLE_NAME = 'developer_webhook_events' AND TG_OP = 'UPDATE'
       AND NEW.id = OLD.id
       AND NEW.owner_id = OLD.owner_id
       AND NEW.event_type = OLD.event_type
       AND NEW.created_at = OLD.created_at
       AND NEW.resource_type = 'redacted'
       AND NEW.resource_id IS NULL
       AND NEW.source_key = 'deleted:' || OLD.id::text
       AND NEW.payload = jsonb_build_object(
         'schemaVersion', 1,
         'eventId', OLD.id,
         'type', OLD.event_type,
         'createdAt', OLD.created_at
       ) THEN
      RETURN NEW;
    END IF;
  END IF;
  RAISE EXCEPTION 'webhook evidence is immutable';
END;
$$ LANGUAGE plpgsql;
