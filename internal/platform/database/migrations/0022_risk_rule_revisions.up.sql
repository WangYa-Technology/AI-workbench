CREATE TABLE risk_rule_revisions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  version integer NOT NULL UNIQUE CHECK (version > 0),
  parent_revision_id uuid REFERENCES risk_rule_revisions(id),
  name text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 80),
  task_dispute_score integer NOT NULL CHECK (task_dispute_score BETWEEN 0 AND 100),
  transaction_refund_score integer NOT NULL CHECK (transaction_refund_score BETWEEN 0 AND 100),
  medium_threshold integer NOT NULL CHECK (medium_threshold BETWEEN 1 AND 98),
  high_threshold integer NOT NULL CHECK (high_threshold BETWEEN 2 AND 99),
  critical_threshold integer NOT NULL CHECK (critical_threshold BETWEEN 3 AND 100),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 500),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT risk_rule_threshold_order CHECK (medium_threshold < high_threshold AND high_threshold < critical_threshold)
);

CREATE TABLE risk_rule_state (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  active_revision_id uuid NOT NULL UNIQUE REFERENCES risk_rule_revisions(id),
  version integer NOT NULL CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

WITH initial AS (
  INSERT INTO risk_rule_revisions(version,name,task_dispute_score,transaction_refund_score,medium_threshold,high_threshold,critical_threshold,reason)
  VALUES(1,'Initial transaction risk rules',85,55,40,70,90,'Initial revision preserves verified task-dispute and Local Test refund severity behavior.')
  RETURNING id
)
INSERT INTO risk_rule_state(singleton,active_revision_id,version)
SELECT true,id,1 FROM initial;

CREATE FUNCTION reject_risk_rule_revision_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'risk rule revisions are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER risk_rule_revisions_immutable BEFORE UPDATE OR DELETE ON risk_rule_revisions
  FOR EACH ROW EXECUTE FUNCTION reject_risk_rule_revision_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('admin:risk_rules','admin','Manage immutable transaction risk rule revisions','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('admin','admin:risk_rules')
ON CONFLICT DO NOTHING;
