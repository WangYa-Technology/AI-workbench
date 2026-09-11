CREATE TABLE identity_auth_challenges (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email_snapshot text NOT NULL CHECK (char_length(email_snapshot) BETWEEN 3 AND 254),
  purpose text NOT NULL CHECK (purpose IN ('login_code','registration_code')),
  locale text NOT NULL CHECK (locale IN ('en-US','zh-CN')),
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','delivered','consumed','expired','cancelled','dead_letter')),
  code_hash text CHECK (code_hash IS NULL OR code_hash ~ '^[0-9a-f]{64}$'),
  code_nonce bytea CHECK (code_nonce IS NULL OR octet_length(code_nonce)=12),
  code_ciphertext bytea CHECK (code_ciphertext IS NULL OR octet_length(code_ciphertext)>16),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 15),
  verify_attempt_count integer NOT NULL DEFAULT 0 CHECK (verify_attempt_count BETWEEN 0 AND 10),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  delivered_at timestamptz,
  consumed_at timestamptz,
  cancelled_at timestamptz,
  dead_lettered_at timestamptz,
  CHECK ((code_hash IS NULL AND code_nonce IS NULL AND code_ciphertext IS NULL) OR
         (code_hash IS NOT NULL AND code_nonce IS NOT NULL AND code_ciphertext IS NOT NULL)),
  CHECK (expires_at > created_at),
  CHECK (consumed_at IS NULL OR status='consumed'),
  CHECK (cancelled_at IS NULL OR status='cancelled'),
  CHECK (dead_lettered_at IS NULL OR status='dead_letter')
);

CREATE UNIQUE INDEX identity_auth_challenges_active_unique
  ON identity_auth_challenges(lower(email_snapshot),purpose)
  WHERE status IN ('queued','delivered');
CREATE UNIQUE INDEX identity_auth_challenges_code_unique
  ON identity_auth_challenges(code_hash);
CREATE INDEX identity_auth_challenges_email_idx
  ON identity_auth_challenges(lower(email_snapshot),created_at DESC);
CREATE INDEX identity_auth_challenges_expiry_idx
  ON identity_auth_challenges(expires_at)
  WHERE status IN ('queued','delivered');

CREATE TABLE identity_auth_challenge_delivery_attempts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  challenge_id uuid NOT NULL REFERENCES identity_auth_challenges(id) ON DELETE CASCADE,
  attempt_number integer NOT NULL CHECK (attempt_number BETWEEN 1 AND 15),
  adapter text NOT NULL CHECK (adapter IN ('local_file','disabled')),
  status text NOT NULL CHECK (status IN ('delivered','failed')),
  error_code text CHECK (error_code IS NULL OR char_length(error_code) BETWEEN 1 AND 80),
  receipt_sha256 text CHECK (receipt_sha256 IS NULL OR receipt_sha256 ~ '^[0-9a-f]{64}$'),
  attempted_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(challenge_id,attempt_number),
  CHECK ((status='delivered') = (receipt_sha256 IS NOT NULL)),
  CHECK ((status='failed') = (error_code IS NOT NULL))
);

CREATE FUNCTION reject_identity_auth_challenge_attempt_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'identity auth challenge delivery evidence is immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER identity_auth_challenge_delivery_attempts_immutable
BEFORE UPDATE OR DELETE ON identity_auth_challenge_delivery_attempts
FOR EACH ROW EXECUTE FUNCTION reject_identity_auth_challenge_attempt_mutation();
