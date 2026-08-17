ALTER TABLE generations
  ADD COLUMN model_route_revision_id uuid,
  ADD COLUMN model_route_version integer;

CREATE TABLE model_route_revisions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  mode text NOT NULL CHECK (mode IN ('chat','image','video','music')),
  version integer NOT NULL CHECK (version > 0),
  parent_revision_id uuid REFERENCES model_route_revisions(id),
  provider_profile_id text NOT NULL REFERENCES provider_profiles(id),
  name text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 80),
  timeout_seconds integer NOT NULL CHECK (timeout_seconds BETWEEN 5 AND 600),
  max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 5),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 500),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(mode,version)
);

CREATE TABLE model_route_state (
  mode text PRIMARY KEY CHECK (mode IN ('chat','image','video','music')),
  active_revision_id uuid NOT NULL UNIQUE REFERENCES model_route_revisions(id),
  version integer NOT NULL CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

WITH initial AS (
  INSERT INTO model_route_revisions(mode,version,provider_profile_id,name,timeout_seconds,max_attempts,reason)
  SELECT p.mode,1,p.id,'Initial '||p.mode||' route',120,3,
         'Initial revision preserves the verified deterministic Local Test model route.'
  FROM provider_profiles p WHERE p.local_test=true
  RETURNING id,mode,version
)
INSERT INTO model_route_state(mode,active_revision_id,version)
SELECT mode,id,version FROM initial;

CREATE FUNCTION reject_model_route_revision_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'model route revisions are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER model_route_revisions_immutable BEFORE UPDATE OR DELETE ON model_route_revisions
  FOR EACH ROW EXECUTE FUNCTION reject_model_route_revision_mutation();

ALTER TABLE generations
  ADD CONSTRAINT generations_model_route_revision_fk FOREIGN KEY (model_route_revision_id) REFERENCES model_route_revisions(id),
  ADD CONSTRAINT generations_model_route_evidence CHECK (
    (model_route_revision_id IS NULL AND model_route_version IS NULL) OR
    (model_route_revision_id IS NOT NULL AND model_route_version > 0)
  );

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('admin:models','admin','Manage immutable model routing revisions','high',true)
ON CONFLICT (id) DO NOTHING;
INSERT INTO role_permissions(role,permission_id) VALUES ('admin','admin:models') ON CONFLICT DO NOTHING;
