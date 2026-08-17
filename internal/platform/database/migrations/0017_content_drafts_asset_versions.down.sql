DROP INDEX IF EXISTS works_owner_asset_active_draft_idx;
ALTER TABLE posts DROP COLUMN IF EXISTS version;
ALTER TABLE works DROP COLUMN IF EXISTS version;

DROP TRIGGER IF EXISTS asset_version_events_immutable ON asset_version_events;
DROP FUNCTION IF EXISTS reject_asset_version_event_mutation();
DROP TABLE IF EXISTS asset_version_events;

DROP INDEX IF EXISTS assets_family_created_idx;
DROP TRIGGER IF EXISTS assets_initialize_family_root ON assets;
DROP FUNCTION IF EXISTS initialize_asset_family_root();
ALTER TABLE assets
  DROP CONSTRAINT IF EXISTS assets_family_version_key,
  DROP CONSTRAINT IF EXISTS assets_version_shape,
  DROP CONSTRAINT IF EXISTS assets_family_root_fk,
  DROP COLUMN IF EXISTS supersedes_asset_id,
  DROP COLUMN IF EXISTS version_note,
  DROP COLUMN IF EXISTS version_number,
  DROP COLUMN IF EXISTS family_id;
