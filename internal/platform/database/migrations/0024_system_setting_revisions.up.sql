CREATE TABLE system_setting_revisions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  version integer NOT NULL UNIQUE CHECK (version > 0),
  parent_revision_id uuid REFERENCES system_setting_revisions(id),
  name text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 80),
  registrations_enabled boolean NOT NULL,
  generations_enabled boolean NOT NULL,
  publishing_enabled boolean NOT NULL,
  marketplace_checkout_enabled boolean NOT NULL,
  task_creation_enabled boolean NOT NULL,
  public_notice text NOT NULL DEFAULT '' CHECK (char_length(public_notice) <= 240),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 500),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE system_setting_state (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  active_revision_id uuid NOT NULL UNIQUE REFERENCES system_setting_revisions(id),
  version integer NOT NULL CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

WITH initial AS (
  INSERT INTO system_setting_revisions(version,name,registrations_enabled,generations_enabled,publishing_enabled,marketplace_checkout_enabled,task_creation_enabled,reason)
  VALUES(1,'Initial platform availability',true,true,true,true,true,'Initial revision preserves the verified local product availability behavior.') RETURNING id
)
INSERT INTO system_setting_state(singleton,active_revision_id,version) SELECT true,id,1 FROM initial;

CREATE FUNCTION reject_system_setting_revision_mutation() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'system setting revisions are immutable'; END; $$ LANGUAGE plpgsql;
CREATE TRIGGER system_setting_revisions_immutable BEFORE UPDATE OR DELETE ON system_setting_revisions FOR EACH ROW EXECUTE FUNCTION reject_system_setting_revision_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES ('admin:settings','admin','Manage immutable platform availability revisions','high',true) ON CONFLICT (id) DO NOTHING;
INSERT INTO role_permissions(role,permission_id) VALUES ('admin','admin:settings') ON CONFLICT DO NOTHING;
