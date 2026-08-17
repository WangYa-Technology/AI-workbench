CREATE OR REPLACE FUNCTION reject_support_evidence_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'support evidence is append-only';
END;
$$ LANGUAGE plpgsql;

ALTER TABLE support_cases
  DROP COLUMN IF EXISTS personal_data_redacted_at;
