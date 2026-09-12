-- pgcrypto functions are referenced with the public schema qualifier. Keep
-- the extension there even when application tests use a custom search_path.
CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;

-- Older databases may have created pgcrypto in the application's search_path.
-- Move it explicitly so public.digest remains available for every migration.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgcrypto') THEN
    ALTER EXTENSION pgcrypto SET SCHEMA public;
  END IF;
END
$$;
