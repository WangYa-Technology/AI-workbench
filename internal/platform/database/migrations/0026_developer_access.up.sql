CREATE TABLE developer_access_control (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  enabled boolean NOT NULL DEFAULT false,
  max_service_accounts integer NOT NULL DEFAULT 5 CHECK (max_service_accounts BETWEEN 1 AND 20),
  max_active_keys integer NOT NULL DEFAULT 3 CHECK (max_active_keys BETWEEN 1 AND 10),
  default_ttl_days integer NOT NULL DEFAULT 90 CHECK (default_ttl_days BETWEEN 1 AND 365),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO developer_access_control(singleton) VALUES(true);

CREATE TABLE developer_service_accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 80),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  CHECK ((status='revoked') = (revoked_at IS NOT NULL))
);
CREATE UNIQUE INDEX developer_service_accounts_owner_name_unique ON developer_service_accounts(owner_id,lower(name));
CREATE INDEX developer_service_accounts_owner_idx ON developer_service_accounts(owner_id,created_at DESC);

CREATE TABLE developer_api_keys (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  service_account_id uuid NOT NULL REFERENCES developer_service_accounts(id) ON DELETE CASCADE,
  public_prefix text NOT NULL UNIQUE CHECK (public_prefix ~ '^[A-Za-z0-9_-]{10,24}$'),
  secret_hash text NOT NULL CHECK (secret_hash ~ '^[0-9a-f]{64}$'),
  display_hint text NOT NULL CHECK (display_hint ~ '^.{0,12}$'),
  scopes text[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 5 AND scopes <@ ARRAY['developer:identity:read']::text[]),
  ip_allowlist text[] NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','rotated','revoked')),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  usage_count bigint NOT NULL DEFAULT 0 CHECK (usage_count >= 0),
  last_used_at timestamptz,
  last_ip_hash text CHECK (last_ip_hash IS NULL OR last_ip_hash ~ '^[0-9a-f]{64}$'),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  rotated_to_id uuid REFERENCES developer_api_keys(id),
  CHECK ((status='active' AND revoked_at IS NULL) OR (status<>'active' AND revoked_at IS NOT NULL))
);
CREATE INDEX developer_api_keys_account_idx ON developer_api_keys(service_account_id,created_at DESC);
CREATE INDEX developer_api_keys_active_idx ON developer_api_keys(public_prefix) WHERE status='active';

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('developer:credentials','developer','Manage personal Service Accounts and API keys','high',true),
  ('admin:developer','admin','Control and inspect Developer Access','high',false)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id) VALUES
  ('member','developer:credentials'),('creator','developer:credentials'),('publisher','developer:credentials'),('admin','developer:credentials'),
  ('admin','admin:developer')
ON CONFLICT DO NOTHING;
