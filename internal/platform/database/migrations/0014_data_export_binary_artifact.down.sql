SELECT set_config('app.data_rights_maintenance','on',true);

ALTER TABLE data_rights_export_artifacts
  ALTER COLUMN body TYPE jsonb USING convert_from(body,'UTF8')::jsonb;

UPDATE data_rights_export_artifacts
SET checksum_sha256=encode(public.digest(convert_to(body::text,'UTF8'),'sha256'),'hex'),size_bytes=octet_length(convert_to(body::text,'UTF8'))
WHERE body IS NOT NULL;
