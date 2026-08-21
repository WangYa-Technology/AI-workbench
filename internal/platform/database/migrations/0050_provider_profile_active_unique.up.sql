ALTER TABLE provider_profiles DROP CONSTRAINT IF EXISTS provider_profiles_mode_provider_model_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS provider_profiles_active_mode_provider_model_name_idx
  ON provider_profiles(mode, provider, model_name)
  WHERE admin_enabled = true;
