SELECT set_config('app.data_rights_maintenance','on',true);

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema=current_schema() AND table_name='data_rights_export_artifacts'
      AND column_name='body' AND data_type='jsonb'
  ) THEN
    ALTER TABLE data_rights_export_artifacts
      ALTER COLUMN body TYPE bytea USING convert_to(body::text,'UTF8');
  END IF;
END;
$$;

UPDATE data_rights_export_artifacts
SET checksum_sha256=encode(public.digest(body,'sha256'),'hex'),size_bytes=octet_length(body)
WHERE body IS NOT NULL;
