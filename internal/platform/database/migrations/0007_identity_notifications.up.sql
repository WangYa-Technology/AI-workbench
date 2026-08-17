ALTER TABLE users
  ADD COLUMN email_verified_at timestamptz;

ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users
  ADD CONSTRAINT users_role_check CHECK (role IN ('member','creator','publisher','moderator','admin'));

ALTER TABLE sessions
  ADD COLUMN client_label text NOT NULL DEFAULT 'Unknown client',
  ADD COLUMN network_hash text,
  ADD COLUMN last_seen_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX sessions_user_activity_idx ON sessions (user_id, last_seen_at DESC);

CREATE TABLE permissions (
  id text PRIMARY KEY,
  module text NOT NULL,
  description text NOT NULL,
  risk_level text NOT NULL CHECK (risk_level IN ('low','medium','high')),
  resource_authorization boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
  role text NOT NULL CHECK (role IN ('member','creator','publisher','moderator','admin')),
  permission_id text NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
  PRIMARY KEY (role, permission_id)
);

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('account:self','identity','Read and update the current account','low',true),
  ('notifications:self','notifications','Read and manage the current account notification inbox','low',true),
  ('creation:submit','creation','Submit AI creation jobs','medium',true),
  ('task:publish','tasks','Publish production briefs','medium',true),
  ('task:propose','tasks','Submit proposals to eligible briefs','medium',true),
  ('task:review','tasks','Review deliveries for owned briefs','high',true),
  ('community:publish','community','Publish owned clean assets','medium',true),
  ('admin:access','admin','Open the operations workspace','high',false);

INSERT INTO role_permissions(role,permission_id) VALUES
  ('member','account:self'),('member','notifications:self'),('member','creation:submit'),('member','task:publish'),('member','community:publish'),
  ('creator','account:self'),('creator','notifications:self'),('creator','creation:submit'),('creator','task:propose'),('creator','community:publish'),
  ('publisher','account:self'),('publisher','notifications:self'),('publisher','creation:submit'),('publisher','task:publish'),('publisher','task:review'),('publisher','community:publish'),
  ('moderator','account:self'),('moderator','notifications:self'),('moderator','admin:access'),
  ('admin','account:self'),('admin','notifications:self'),('admin','creation:submit'),('admin','task:publish'),('admin','task:propose'),('admin','task:review'),('admin','community:publish'),('admin','admin:access');

CREATE TABLE oauth_provider_configs (
  provider text PRIMARY KEY CHECK (provider IN ('google','github')),
  display_name text NOT NULL,
  enabled boolean NOT NULL DEFAULT false,
  client_id text,
  redirect_uri text,
  secret_reference text,
  scopes text[] NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO oauth_provider_configs(provider,display_name,scopes) VALUES
  ('google','Google',ARRAY['openid','email','profile']),
  ('github','GitHub',ARRAY['read:user','user:email']);

CREATE TABLE oauth_accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider text NOT NULL REFERENCES oauth_provider_configs(provider),
  provider_user_id text NOT NULL,
  provider_email text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_user_id),
  UNIQUE (user_id, provider)
);

ALTER TABLE notifications
  ADD COLUMN resource_type text,
  ADD COLUMN resource_id uuid,
  ADD COLUMN source_key text;

ALTER TABLE notifications
  ADD CONSTRAINT notifications_target_path_check
  CHECK (target_path ~ '^/[a-z0-9/_?=&.%:-]*$' AND target_path !~ '^//');

CREATE UNIQUE INDEX notifications_user_source_key_unique
  ON notifications(user_id, source_key) WHERE source_key IS NOT NULL;
CREATE INDEX notifications_user_inbox_idx ON notifications(user_id, created_at DESC);
CREATE INDEX notifications_user_unread_idx ON notifications(user_id, created_at DESC) WHERE read_at IS NULL;

CREATE TABLE notification_preferences (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  notification_kind text NOT NULL,
  in_app_enabled boolean NOT NULL DEFAULT true,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, notification_kind)
);
