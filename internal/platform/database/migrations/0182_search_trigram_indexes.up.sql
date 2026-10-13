CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

-- public_works is a view; indexes must be defined on its underlying table.
CREATE INDEX discovery_works_title_trgm ON works USING gin (title public.gin_trgm_ops);
CREATE INDEX discovery_works_summary_trgm ON works USING gin (summary public.gin_trgm_ops);
CREATE INDEX discovery_works_model_name_trgm ON works USING gin (model_name public.gin_trgm_ops);
CREATE INDEX discovery_assets_license_code_trgm ON assets USING gin (license_code public.gin_trgm_ops);
CREATE INDEX discovery_users_display_name_trgm ON users USING gin (display_name public.gin_trgm_ops);
CREATE INDEX discovery_users_handle_trgm ON users USING gin (handle public.gin_trgm_ops);

CREATE INDEX community_posts_title_trgm ON posts USING gin (title public.gin_trgm_ops);
CREATE INDEX community_posts_body_trgm ON posts USING gin (body public.gin_trgm_ops);
