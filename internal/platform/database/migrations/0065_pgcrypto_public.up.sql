-- pgcrypto functions are referenced with the public schema qualifier. Keep
-- the extension there even when application tests use a custom search_path.
CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;
