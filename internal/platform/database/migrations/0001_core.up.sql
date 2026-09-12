CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;

CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL,
  handle text NOT NULL,
  display_name text NOT NULL,
  password_hash text,
  role text NOT NULL DEFAULT 'member' CHECK (role IN ('member','creator','moderator','admin')),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
  locale text NOT NULL DEFAULT 'en-US',
  timezone text NOT NULL DEFAULT 'UTC',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
CREATE UNIQUE INDEX users_handle_lower_key ON users (lower(handle));

CREATE TABLE sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE assets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id uuid NOT NULL REFERENCES users(id),
  kind text NOT NULL CHECK (kind IN ('image','video','audio','document','prompt','workflow')),
  title text NOT NULL,
  media_url text NOT NULL,
  mime_type text NOT NULL,
  width integer CHECK (width IS NULL OR width > 0),
  height integer CHECK (height IS NULL OR height > 0),
  scan_status text NOT NULL DEFAULT 'clean' CHECK (scan_status IN ('pending','clean','review','rejected')),
  source_type text NOT NULL CHECK (source_type IN ('upload','generation','purchase','delivery','demo')),
  source_id uuid,
  license_code text NOT NULL DEFAULT 'personal',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE works (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  author_id uuid NOT NULL REFERENCES users(id),
  asset_id uuid NOT NULL REFERENCES assets(id),
  title text NOT NULL,
  summary text NOT NULL DEFAULT '',
  prompt text,
  prompt_visibility text NOT NULL DEFAULT 'public' CHECK (prompt_visibility IN ('public','partial','purchased','private')),
  model_name text NOT NULL,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','hidden','removed')),
  ai_disclosure text NOT NULL,
  published_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX works_published_idx ON works (published_at DESC) WHERE status='published';

CREATE TABLE generations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id uuid NOT NULL REFERENCES users(id),
  mode text NOT NULL CHECK (mode IN ('chat','image','video','music')),
  provider text NOT NULL,
  model_name text NOT NULL,
  prompt text NOT NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','cancelled')),
  progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
  estimated_cost_cents integer NOT NULL DEFAULT 0 CHECK (estimated_cost_cents >= 0),
  charged_cost_cents integer NOT NULL DEFAULT 0 CHECK (charged_cost_cents >= 0),
  output_asset_id uuid REFERENCES assets(id),
  source_work_id uuid REFERENCES works(id),
  error_code text,
  error_message text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE posts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  author_id uuid NOT NULL REFERENCES users(id),
  work_id uuid REFERENCES works(id),
  body text NOT NULL,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','hidden','removed')),
  published_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE comments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  post_id uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  author_id uuid NOT NULL REFERENCES users(id),
  body text NOT NULL,
  status text NOT NULL DEFAULT 'published' CHECK (status IN ('published','hidden','removed')),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE products (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id uuid NOT NULL REFERENCES users(id),
  asset_id uuid NOT NULL REFERENCES assets(id),
  title text NOT NULL,
  description text NOT NULL,
  product_type text NOT NULL CHECK (product_type IN ('prompt','workflow','asset','work')),
  price_cents integer NOT NULL CHECK (price_cents >= 0),
  currency text NOT NULL DEFAULT 'USD' CHECK (char_length(currency)=3),
  license_code text NOT NULL,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','paused','removed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE orders (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  buyer_id uuid NOT NULL REFERENCES users(id),
  product_id uuid NOT NULL REFERENCES products(id),
  amount_cents integer NOT NULL CHECK (amount_cents >= 0),
  currency text NOT NULL CHECK (char_length(currency)=3),
  status text NOT NULL DEFAULT 'test_pending' CHECK (status IN ('test_pending','test_paid','fulfilled','refund_requested','test_refunded','cancelled')),
  license_accepted_at timestamptz,
  idempotency_key text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE demands (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  client_id uuid NOT NULL REFERENCES users(id),
  title text NOT NULL,
  brief text NOT NULL,
  deliverable_type text NOT NULL CHECK (deliverable_type IN ('image','video','audio','prompt','workflow','mixed')),
  budget_cents integer NOT NULL CHECK (budget_cents > 0),
  currency text NOT NULL DEFAULT 'USD' CHECK (char_length(currency)=3),
  deadline timestamptz NOT NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('draft','open','assigned','submitted','revision','accepted','disputed','cancelled')),
  assignee_id uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE proposals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  demand_id uuid NOT NULL REFERENCES demands(id) ON DELETE CASCADE,
  creator_id uuid NOT NULL REFERENCES users(id),
  approach text NOT NULL,
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  status text NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted','accepted','rejected','withdrawn')),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (demand_id, creator_id)
);

CREATE TABLE deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  demand_id uuid NOT NULL REFERENCES demands(id),
  creator_id uuid NOT NULL REFERENCES users(id),
  asset_id uuid NOT NULL REFERENCES assets(id),
  note text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted','revision','accepted','disputed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES users(id),
  operation_id uuid NOT NULL,
  direction text NOT NULL CHECK (direction IN ('debit','credit')),
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL CHECK (char_length(currency)=3),
  reason text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind text NOT NULL,
  title text NOT NULL,
  body text NOT NULL,
  target_path text NOT NULL,
  read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_id uuid REFERENCES users(id),
  action text NOT NULL,
  resource_type text NOT NULL,
  resource_id uuid,
  reason text,
  request_id text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  kind text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','cancelled')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
  available_at timestamptz NOT NULL DEFAULT now(),
  lease_owner text,
  lease_expires_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX jobs_claim_idx ON jobs (available_at, created_at) WHERE status='queued';
