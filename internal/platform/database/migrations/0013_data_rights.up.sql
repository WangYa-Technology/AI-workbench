CREATE TABLE data_rights_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id),
  request_type text NOT NULL CHECK (request_type IN ('data_export','account_deletion')),
  status text NOT NULL CHECK (status IN ('queued','ready','scheduled','processing','blocked','completed','cancelled','failed')),
  subject_ref text NOT NULL CHECK (subject_ref ~ '^subject_[a-f0-9]{24}$'),
  execute_after timestamptz NOT NULL,
  cancel_until timestamptz,
  completed_at timestamptz,
  failure_code text,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX data_rights_active_type_idx ON data_rights_requests(user_id,request_type)
  WHERE status IN ('queued','ready','scheduled','processing','blocked');
CREATE INDEX data_rights_owner_created_idx ON data_rights_requests(user_id,created_at DESC);
CREATE INDEX data_rights_due_idx ON data_rights_requests(execute_after,created_at)
  WHERE status IN ('queued','scheduled','blocked');

CREATE TABLE data_rights_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id uuid NOT NULL REFERENCES data_rights_requests(id),
  actor_id uuid REFERENCES users(id),
  event_type text NOT NULL,
  from_status text,
  to_status text NOT NULL,
  reason text NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX data_rights_events_request_idx ON data_rights_events(request_id,created_at,id);

CREATE TABLE data_rights_export_artifacts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id uuid NOT NULL UNIQUE REFERENCES data_rights_requests(id),
  body jsonb,
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[a-f0-9]{64}$'),
  size_bytes bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 5242880),
  expires_at timestamptz NOT NULL,
  purged_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE data_rights_deletion_receipts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id uuid NOT NULL UNIQUE REFERENCES data_rights_requests(id),
  subject_ref text NOT NULL CHECK (subject_ref ~ '^subject_[a-f0-9]{24}$'),
  receipt jsonb NOT NULL,
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[a-f0-9]{64}$'),
  completed_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE data_rights_legal_holds (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id),
  request_id uuid REFERENCES data_rights_requests(id),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 1000),
  authority_reference_hash text NOT NULL CHECK (authority_reference_hash ~ '^[a-f0-9]{64}$'),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','released','expired')),
  review_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  created_by uuid NOT NULL REFERENCES users(id),
  released_by uuid REFERENCES users(id),
  released_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (review_at < expires_at)
);
CREATE UNIQUE INDEX data_rights_active_hold_idx ON data_rights_legal_holds(user_id) WHERE status='active';

CREATE OR REPLACE FUNCTION reject_data_rights_evidence_mutation() RETURNS trigger AS $$
BEGIN
  IF current_setting('app.data_rights_maintenance', true) = 'on' THEN
    RETURN COALESCE(NEW, OLD);
  END IF;
  RAISE EXCEPTION 'data rights evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER data_rights_events_immutable BEFORE UPDATE OR DELETE ON data_rights_events
  FOR EACH ROW EXECUTE FUNCTION reject_data_rights_evidence_mutation();
CREATE TRIGGER data_rights_artifacts_immutable BEFORE UPDATE OR DELETE ON data_rights_export_artifacts
  FOR EACH ROW EXECUTE FUNCTION reject_data_rights_evidence_mutation();
CREATE TRIGGER data_rights_receipts_immutable BEFORE UPDATE OR DELETE ON data_rights_deletion_receipts
  FOR EACH ROW EXECUTE FUNCTION reject_data_rights_evidence_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('account:data-rights','identity','Request and inspect personal data export or account deletion','high',true),
  ('admin:data-rights','admin','Inspect data-rights requests and control legal holds','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
SELECT role,'account:data-rights' FROM (VALUES ('member'),('creator'),('publisher'),('moderator'),('admin')) roles(role)
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions(role,permission_id) VALUES ('admin','admin:data-rights') ON CONFLICT DO NOTHING;
