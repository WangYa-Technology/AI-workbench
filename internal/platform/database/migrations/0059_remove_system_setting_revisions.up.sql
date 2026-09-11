CREATE TABLE system_settings (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  registrations_enabled boolean NOT NULL,
  generations_enabled boolean NOT NULL,
  publishing_enabled boolean NOT NULL,
  marketplace_checkout_enabled boolean NOT NULL,
  task_creation_enabled boolean NOT NULL,
  public_notice text NOT NULL DEFAULT '' CHECK (char_length(public_notice) <= 240),
  site_configuration jsonb NOT NULL CHECK (jsonb_typeof(site_configuration) = 'object'),
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO system_settings(
  singleton,
  id,
  registrations_enabled,
  generations_enabled,
  publishing_enabled,
  marketplace_checkout_enabled,
  task_creation_enabled,
  public_notice,
  site_configuration,
  updated_by,
  updated_at
)
SELECT
  true,
  revision.id,
  revision.registrations_enabled,
  revision.generations_enabled,
  revision.publishing_enabled,
  revision.marketplace_checkout_enabled,
  revision.task_creation_enabled,
  revision.public_notice,
  revision.site_configuration,
  revision.created_by,
  revision.created_at
FROM system_setting_state state
JOIN system_setting_revisions revision ON revision.id = state.active_revision_id
WHERE state.singleton = true;

DROP TABLE system_setting_state;
DROP TRIGGER system_setting_revisions_immutable ON system_setting_revisions;
DROP FUNCTION reject_system_setting_revision_mutation();
DROP TABLE system_setting_revisions;

UPDATE permissions
SET description = 'Manage current platform availability and public site configuration'
WHERE id = 'admin:settings';
