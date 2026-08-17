ALTER TABLE support_cases
  ADD COLUMN personal_data_redacted_at timestamptz;

CREATE OR REPLACE FUNCTION reject_support_evidence_mutation() RETURNS trigger AS $$
BEGIN
  IF current_setting('app.support_data_rights_maintenance', true) = 'on' AND TG_OP = 'UPDATE' THEN
    IF TG_TABLE_NAME = 'support_messages'
       AND to_jsonb(NEW) - 'body' = to_jsonb(OLD) - 'body'
       AND to_jsonb(NEW)->>'body' = '[Redacted following account deletion]' THEN
      RETURN NEW;
    END IF;

    IF TG_TABLE_NAME = 'support_events'
       AND to_jsonb(NEW) - 'reason' - 'metadata' = to_jsonb(OLD) - 'reason' - 'metadata'
       AND to_jsonb(NEW)->>'reason' = 'Redacted following account deletion.'
       AND to_jsonb(NEW)->'metadata' = '{}'::jsonb THEN
      RETURN NEW;
    END IF;
  END IF;

  RAISE EXCEPTION 'support evidence is append-only';
END;
$$ LANGUAGE plpgsql;
