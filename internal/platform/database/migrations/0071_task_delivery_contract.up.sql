ALTER TABLE demands ADD COLUMN allow_derivative_reuse boolean NOT NULL DEFAULT false;
ALTER TABLE deliveries
  ADD COLUMN rights_evidence text NOT NULL DEFAULT '',
  ADD COLUMN ai_disclosure text NOT NULL DEFAULT '';

CREATE TABLE delivery_assets (
  delivery_id uuid NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
  asset_id uuid NOT NULL REFERENCES assets(id),
  position integer NOT NULL CHECK (position BETWEEN 0 AND 19),
  PRIMARY KEY(delivery_id,asset_id),
  UNIQUE(delivery_id,position)
);
INSERT INTO delivery_assets(delivery_id,asset_id,position) SELECT id,asset_id,0 FROM deliveries;

CREATE TABLE task_delivery_grants (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  demand_id uuid NOT NULL REFERENCES demands(id),
  delivery_id uuid NOT NULL REFERENCES deliveries(id),
  source_asset_id uuid NOT NULL REFERENCES assets(id),
  asset_id uuid NOT NULL UNIQUE REFERENCES assets(id),
  client_id uuid NOT NULL REFERENCES users(id),
  creator_id uuid NOT NULL REFERENCES users(id),
  rights_terms text NOT NULL,
  rights_evidence text NOT NULL,
  ai_disclosure text NOT NULL,
  allow_derivative_reuse boolean NOT NULL,
  granted_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(delivery_id,source_asset_id)
);
CREATE INDEX task_delivery_grants_client ON task_delivery_grants(client_id,granted_at DESC);
CREATE FUNCTION reject_task_grant_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'task delivery grant evidence is immutable';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER task_delivery_grants_immutable BEFORE UPDATE OR DELETE ON task_delivery_grants
  FOR EACH ROW EXECUTE FUNCTION reject_task_grant_mutation();
