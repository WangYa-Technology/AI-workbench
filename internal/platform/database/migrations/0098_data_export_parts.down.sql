DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM data_rights_export_artifacts WHERE part_count > 0)
    OR EXISTS (SELECT 1 FROM data_rights_export_parts) THEN
    RAISE EXCEPTION 'cannot discard multipart export evidence';
  END IF;
END;
$$;
DROP TABLE data_rights_export_parts;
DROP FUNCTION validate_data_export_part();
ALTER TABLE data_rights_export_artifacts
  DROP CONSTRAINT data_rights_export_artifacts_format_check,
  DROP COLUMN part_count,
  DROP CONSTRAINT data_rights_export_artifacts_size_bytes_check,
  ADD CONSTRAINT data_rights_export_artifacts_size_bytes_check CHECK (size_bytes > 0 AND size_bytes <= 5242880);
