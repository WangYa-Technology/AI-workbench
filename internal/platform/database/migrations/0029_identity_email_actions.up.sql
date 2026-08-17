CREATE TABLE identity_email_actions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('verify_email','password_reset')),
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','delivered','consumed','expired','cancelled','dead_letter')),
  email_snapshot text NOT NULL CHECK (char_length(email_snapshot) BETWEEN 3 AND 254),
  locale text NOT NULL CHECK (locale IN ('en-US','zh-CN')),
  token_hash text CHECK (token_hash IS NULL OR token_hash ~ '^[0-9a-f]{64}$'),
  token_nonce bytea CHECK (token_nonce IS NULL OR octet_length(token_nonce)=12),
  token_ciphertext bytea CHECK (token_ciphertext IS NULL OR octet_length(token_ciphertext)>16),
  original_action_id uuid REFERENCES identity_email_actions(id),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 15),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  delivered_at timestamptz,
  consumed_at timestamptz,
  cancelled_at timestamptz,
  dead_lettered_at timestamptz,
  CHECK ((token_hash IS NULL AND token_nonce IS NULL AND token_ciphertext IS NULL) OR
         (token_hash IS NOT NULL AND token_nonce IS NOT NULL AND token_ciphertext IS NOT NULL)),
  CHECK (expires_at > created_at),
  CHECK (consumed_at IS NULL OR status='consumed'),
  CHECK (cancelled_at IS NULL OR status='cancelled'),
  CHECK (dead_lettered_at IS NULL OR status='dead_letter')
);

CREATE UNIQUE INDEX identity_email_actions_token_unique
  ON identity_email_actions(token_hash) WHERE token_hash IS NOT NULL;
CREATE UNIQUE INDEX identity_email_actions_active_unique
  ON identity_email_actions(user_id,kind) WHERE status IN ('queued','delivered');
CREATE INDEX identity_email_actions_user_idx
  ON identity_email_actions(user_id,created_at DESC);
CREATE INDEX identity_email_actions_dead_letter_idx
  ON identity_email_actions(updated_at DESC) WHERE status='dead_letter';
CREATE INDEX identity_email_actions_expiry_idx
  ON identity_email_actions(expires_at) WHERE status IN ('queued','delivered');

CREATE TABLE identity_email_delivery_attempts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  action_id uuid NOT NULL REFERENCES identity_email_actions(id) ON DELETE CASCADE,
  attempt_number integer NOT NULL CHECK (attempt_number BETWEEN 1 AND 15),
  adapter text NOT NULL CHECK (adapter IN ('local_file','disabled')),
  status text NOT NULL CHECK (status IN ('delivered','failed')),
  error_code text CHECK (error_code IS NULL OR char_length(error_code) BETWEEN 1 AND 80),
  receipt_sha256 text CHECK (receipt_sha256 IS NULL OR receipt_sha256 ~ '^[0-9a-f]{64}$'),
  attempted_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(action_id,attempt_number),
  CHECK ((status='delivered') = (receipt_sha256 IS NOT NULL)),
  CHECK ((status='failed') = (error_code IS NOT NULL))
);

CREATE FUNCTION reject_identity_email_attempt_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'identity email delivery evidence is immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER identity_email_delivery_attempts_immutable
BEFORE UPDATE OR DELETE ON identity_email_delivery_attempts
FOR EACH ROW EXECUTE FUNCTION reject_identity_email_attempt_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('admin:email_delivery','admin','Recover failed identity email actions','high',false);

INSERT INTO role_permissions(role,permission_id) VALUES
  ('admin','admin:email_delivery');
