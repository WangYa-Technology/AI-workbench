-- Keep existing single-body exports readable. New packages use bounded parts;
-- publishing the manifest, parts and ready state is one transaction.
ALTER TABLE data_rights_export_artifacts
  DROP CONSTRAINT data_rights_export_artifacts_size_bytes_check,
  ADD CONSTRAINT data_rights_export_artifacts_size_bytes_check CHECK (size_bytes > 0),
  ADD COLUMN part_count integer NOT NULL DEFAULT 0 CHECK (part_count >= 0),
  ADD CONSTRAINT data_rights_export_artifacts_format_check CHECK
    ((part_count = 0 AND size_bytes <= 5242880)
      OR (part_count > 0 AND body IS NULL AND part_count::bigint = (size_bytes - 1) / 5242880 + 1));

CREATE TABLE data_rights_export_parts (
  request_id uuid NOT NULL REFERENCES data_rights_export_artifacts(request_id),
  part_number integer NOT NULL CHECK (part_number > 0),
  body bytea,
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[a-f0-9]{64}$'),
  size_bytes integer NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 5242880),
  purged_at timestamptz,
  PRIMARY KEY (request_id, part_number),
  CHECK ((body IS NOT NULL AND purged_at IS NULL AND octet_length(body) = size_bytes
    AND encode(public.digest(body, 'sha256'),'hex') = checksum_sha256)
    OR (body IS NULL AND purged_at IS NOT NULL))
);

CREATE TRIGGER data_rights_export_parts_immutable BEFORE UPDATE OR DELETE ON data_rights_export_parts
  FOR EACH ROW EXECUTE FUNCTION reject_data_rights_evidence_mutation();

CREATE FUNCTION validate_data_export_part() RETURNS trigger AS $$
DECLARE
  manifest data_rights_export_artifacts%ROWTYPE;
  request_status text;
BEGIN
  SELECT * INTO STRICT manifest FROM data_rights_export_artifacts WHERE request_id=NEW.request_id;
  SELECT status INTO STRICT request_status FROM data_rights_requests WHERE id=NEW.request_id;
  IF request_status <> 'processing' OR manifest.purged_at IS NOT NULL
    OR NEW.part_number > manifest.part_count
    OR NEW.size_bytes <> LEAST(5242880::bigint, manifest.size_bytes - (NEW.part_number::bigint - 1) * 5242880)
    OR NEW.body IS NULL THEN
    RAISE EXCEPTION 'export part does not match pending manifest';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER data_rights_export_part_manifest BEFORE INSERT ON data_rights_export_parts
  FOR EACH ROW EXECUTE FUNCTION validate_data_export_part();
