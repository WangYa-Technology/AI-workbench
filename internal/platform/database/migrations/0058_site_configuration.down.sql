ALTER TABLE system_setting_revisions
  DROP CONSTRAINT IF EXISTS system_setting_site_configuration_object,
  DROP COLUMN IF EXISTS site_configuration;
