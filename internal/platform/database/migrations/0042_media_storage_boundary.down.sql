DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM assets WHERE storage_backend IS NOT NULL AND storage_backend <> 'local_file') THEN
    RAISE EXCEPTION 'cannot remove media storage evidence while non-local Asset objects exist';
  END IF;
END;
$$;

DROP INDEX IF EXISTS assets_storage_object_unique;
ALTER TABLE assets
  DROP CONSTRAINT IF EXISTS assets_storage_evidence_check,
  DROP CONSTRAINT IF EXISTS assets_storage_backend_check,
  DROP COLUMN IF EXISTS storage_key,
  DROP COLUMN IF EXISTS storage_backend;
