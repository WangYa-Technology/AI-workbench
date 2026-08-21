CREATE TABLE provider_configs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 120),
  protocol text NOT NULL CHECK (protocol IN ('openai_responses','openai_chat_completions','openai_images','hctopup_async_image','custom')),
  endpoint text NOT NULL,
  runtime_provider text NOT NULL DEFAULT 'openai',
  credential_nonce bytea,
  credential_ciphertext bytea,
  credential_hint text,
  admin_enabled boolean NOT NULL DEFAULT true,
  archived_at timestamptz,
  created_by uuid REFERENCES users(id),
  updated_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX provider_configs_name_active_idx ON provider_configs(lower(name)) WHERE archived_at IS NULL;

CREATE TABLE provider_config_models (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES provider_configs(id) ON DELETE CASCADE,
  mode text NOT NULL CHECK (mode IN ('chat','image','video','music')),
  model_name text NOT NULL CHECK (char_length(model_name) BETWEEN 1 AND 160),
  display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 2 AND 120),
  description text NOT NULL DEFAULT '',
  estimated_cost_cents integer NOT NULL DEFAULT 0 CHECK (estimated_cost_cents >= 0),
  admin_enabled boolean NOT NULL DEFAULT true,
  archived_at timestamptz,
  created_by uuid REFERENCES users(id),
  updated_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider_id, mode, model_name)
);
CREATE INDEX provider_config_models_active_idx ON provider_config_models(provider_id, mode) WHERE archived_at IS NULL;
