CREATE TABLE developer_webhook_endpoints (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 80),
  url text NOT NULL CHECK (char_length(url) BETWEEN 8 AND 2048),
  event_types text[] NOT NULL CHECK (
    cardinality(event_types) BETWEEN 1 AND 5 AND
    event_types <@ ARRAY[
      'developer.webhook.test',
      'generation.completed',
      'work.published',
      'marketplace.order.fulfilled',
      'marketplace.order.refunded'
    ]::text[]
  ),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
  current_secret_version integer NOT NULL DEFAULT 1 CHECK (current_secret_version > 0),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  CHECK ((status='active') = (revoked_at IS NULL))
);
CREATE UNIQUE INDEX developer_webhook_endpoints_owner_name_unique ON developer_webhook_endpoints(owner_id,lower(name));
CREATE INDEX developer_webhook_endpoints_owner_idx ON developer_webhook_endpoints(owner_id,created_at DESC);

CREATE TABLE developer_webhook_secret_revisions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  endpoint_id uuid NOT NULL REFERENCES developer_webhook_endpoints(id) ON DELETE CASCADE,
  version integer NOT NULL CHECK (version > 0),
  nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
  ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 16),
  display_hint text NOT NULL CHECK (char_length(display_hint) BETWEEN 4 AND 12),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(endpoint_id,version)
);

CREATE TABLE developer_webhook_events (
  id uuid PRIMARY KEY,
  owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  event_type text NOT NULL CHECK (event_type IN (
    'developer.webhook.test',
    'generation.completed',
    'work.published',
    'marketplace.order.fulfilled',
    'marketplace.order.refunded'
  )),
  resource_type text NOT NULL CHECK (char_length(resource_type) BETWEEN 1 AND 80),
  resource_id uuid,
  payload jsonb NOT NULL,
  source_key text NOT NULL CHECK (char_length(source_key) BETWEEN 3 AND 240),
  created_at timestamptz NOT NULL,
  UNIQUE(owner_id,source_key)
);
CREATE INDEX developer_webhook_events_owner_idx ON developer_webhook_events(owner_id,created_at DESC);

CREATE TABLE developer_webhook_deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  endpoint_id uuid NOT NULL REFERENCES developer_webhook_endpoints(id) ON DELETE CASCADE,
  event_id uuid NOT NULL REFERENCES developer_webhook_events(id) ON DELETE CASCADE,
  secret_revision_id uuid NOT NULL REFERENCES developer_webhook_secret_revisions(id),
  original_delivery_id uuid REFERENCES developer_webhook_deliveries(id),
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','delivering','retry_scheduled','succeeded','dead_letter','cancelled')),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 5),
  next_attempt_at timestamptz,
  last_status_code integer CHECK (last_status_code IS NULL OR last_status_code BETWEEN 100 AND 599),
  last_error_code text CHECK (last_error_code IS NULL OR char_length(last_error_code) BETWEEN 1 AND 80),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  succeeded_at timestamptz,
  dead_lettered_at timestamptz
);
CREATE UNIQUE INDEX developer_webhook_deliveries_initial_unique ON developer_webhook_deliveries(endpoint_id,event_id) WHERE original_delivery_id IS NULL;
CREATE INDEX developer_webhook_deliveries_endpoint_idx ON developer_webhook_deliveries(endpoint_id,created_at DESC);
CREATE INDEX developer_webhook_deliveries_dead_letter_idx ON developer_webhook_deliveries(updated_at DESC) WHERE status='dead_letter';

CREATE TABLE developer_webhook_delivery_attempts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  delivery_id uuid NOT NULL REFERENCES developer_webhook_deliveries(id) ON DELETE CASCADE,
  attempt_number integer NOT NULL CHECK (attempt_number BETWEEN 1 AND 5),
  status_code integer CHECK (status_code IS NULL OR status_code BETWEEN 100 AND 599),
  error_code text CHECK (error_code IS NULL OR char_length(error_code) BETWEEN 1 AND 80),
  response_sha256 text CHECK (response_sha256 IS NULL OR response_sha256 ~ '^[0-9a-f]{64}$'),
  duration_ms integer NOT NULL CHECK (duration_ms >= 0),
  attempted_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(delivery_id,attempt_number)
);

CREATE FUNCTION reject_webhook_evidence_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'webhook evidence is immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER developer_webhook_secret_revisions_immutable
BEFORE UPDATE OR DELETE ON developer_webhook_secret_revisions
FOR EACH ROW EXECUTE FUNCTION reject_webhook_evidence_mutation();
CREATE TRIGGER developer_webhook_events_immutable
BEFORE UPDATE OR DELETE ON developer_webhook_events
FOR EACH ROW EXECUTE FUNCTION reject_webhook_evidence_mutation();
CREATE TRIGGER developer_webhook_delivery_attempts_immutable
BEFORE UPDATE OR DELETE ON developer_webhook_delivery_attempts
FOR EACH ROW EXECUTE FUNCTION reject_webhook_evidence_mutation();
