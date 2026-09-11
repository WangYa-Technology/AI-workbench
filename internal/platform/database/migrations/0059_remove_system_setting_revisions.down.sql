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
  site_configuration jsonb NOT NULL CHECK (jsonb_typeof(site_configuration) = 'object'),
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

INSERT INTO system_setting_revisions(
  id,
  version,
  name,
  registrations_enabled,
  generations_enabled,
  publishing_enabled,
  marketplace_checkout_enabled,
  task_creation_enabled,
  public_notice,
  site_configuration,
  reason,
  created_by,
  created_at
)
SELECT
  id,
  1,
  'Restored platform availability',
  registrations_enabled,
  generations_enabled,
  publishing_enabled,
  marketplace_checkout_enabled,
  task_creation_enabled,
  public_notice,
  site_configuration,
  'Restored from mutable system settings during migration rollback.',
  updated_by,
  updated_at
FROM system_settings
WHERE singleton = true;

INSERT INTO system_setting_state(singleton, active_revision_id, version, updated_at)
SELECT true, id, 1, updated_at FROM system_settings WHERE singleton = true;

CREATE FUNCTION reject_system_setting_revision_mutation() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'system setting revisions are immutable'; END; $$ LANGUAGE plpgsql;
CREATE TRIGGER system_setting_revisions_immutable BEFORE UPDATE OR DELETE ON system_setting_revisions FOR EACH ROW EXECUTE FUNCTION reject_system_setting_revision_mutation();

DROP TABLE system_settings;

UPDATE permissions
SET description = 'Manage immutable platform availability revisions'
WHERE id = 'admin:settings';
