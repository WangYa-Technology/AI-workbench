ALTER TABLE assets
  ADD COLUMN family_id uuid,
  ADD COLUMN version_number integer NOT NULL DEFAULT 1 CHECK (version_number > 0),
  ADD COLUMN version_note text CHECK (version_note IS NULL OR char_length(version_note) BETWEEN 3 AND 500),
  ADD COLUMN supersedes_asset_id uuid REFERENCES assets(id);

UPDATE assets SET family_id=id;

ALTER TABLE assets
  ALTER COLUMN family_id SET NOT NULL,
  ADD CONSTRAINT assets_family_root_fk FOREIGN KEY (family_id) REFERENCES assets(id),
  ADD CONSTRAINT assets_version_shape CHECK (
    (version_number = 1 AND supersedes_asset_id IS NULL)
    OR (version_number > 1 AND supersedes_asset_id IS NOT NULL)
  ),
  ADD CONSTRAINT assets_family_version_key UNIQUE (family_id,version_number);

CREATE FUNCTION initialize_asset_family_root() RETURNS trigger AS $$
BEGIN
  IF NEW.family_id IS NULL THEN
    NEW.family_id := NEW.id;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER assets_initialize_family_root BEFORE INSERT ON assets
  FOR EACH ROW EXECUTE FUNCTION initialize_asset_family_root();

CREATE INDEX assets_family_created_idx ON assets(family_id,version_number DESC,created_at DESC);

CREATE TABLE asset_version_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  family_id uuid NOT NULL REFERENCES assets(id),
  asset_id uuid NOT NULL REFERENCES assets(id),
  actor_id uuid NOT NULL REFERENCES users(id),
  previous_asset_id uuid REFERENCES assets(id),
  event_type text NOT NULL CHECK (event_type IN ('version_created')),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 3 AND 500),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX asset_version_events_family_idx ON asset_version_events(family_id,created_at,id);

CREATE FUNCTION reject_asset_version_event_mutation() RETURNS trigger AS $$
BEGIN
  IF current_setting('app.asset_data_rights_maintenance', true) = 'on'
     AND TG_OP = 'UPDATE'
     AND to_jsonb(NEW) - 'reason' = to_jsonb(OLD) - 'reason'
     AND NEW.reason = 'Redacted following account deletion.' THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'asset version evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER asset_version_events_immutable BEFORE UPDATE OR DELETE ON asset_version_events
  FOR EACH ROW EXECUTE FUNCTION reject_asset_version_event_mutation();

ALTER TABLE works ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0);
ALTER TABLE posts ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0);

CREATE UNIQUE INDEX works_owner_asset_active_draft_idx
  ON works(author_id,asset_id) WHERE status='draft';
