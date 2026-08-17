CREATE TABLE discovery_ranking_revisions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  version integer NOT NULL UNIQUE CHECK (version > 0),
  parent_revision_id uuid REFERENCES discovery_ranking_revisions(id),
  name text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 80),
  title_exact_weight integer NOT NULL CHECK (title_exact_weight BETWEEN 0 AND 200),
  title_prefix_weight integer NOT NULL CHECK (title_prefix_weight BETWEEN 0 AND 200),
  title_contains_weight integer NOT NULL CHECK (title_contains_weight BETWEEN 0 AND 200),
  creator_exact_weight integer NOT NULL CHECK (creator_exact_weight BETWEEN 0 AND 200),
  creator_match_weight integer NOT NULL CHECK (creator_match_weight BETWEEN 0 AND 200),
  body_match_weight integer NOT NULL CHECK (body_match_weight BETWEEN 0 AND 200),
  secondary_match_weight integer NOT NULL CHECK (secondary_match_weight BETWEEN 0 AND 200),
  recency_weight integer NOT NULL CHECK (recency_weight BETWEEN 0 AND 50),
  creator_activity_weight integer NOT NULL CHECK (creator_activity_weight BETWEEN 0 AND 50),
  work_type_boost integer NOT NULL CHECK (work_type_boost BETWEEN -50 AND 50),
  creator_type_boost integer NOT NULL CHECK (creator_type_boost BETWEEN -50 AND 50),
  product_type_boost integer NOT NULL CHECK (product_type_boost BETWEEN -50 AND 50),
  demand_type_boost integer NOT NULL CHECK (demand_type_boost BETWEEN -50 AND 50),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 500),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT discovery_ranking_title_order CHECK (title_exact_weight >= title_prefix_weight AND title_prefix_weight >= title_contains_weight),
  CONSTRAINT discovery_ranking_creator_order CHECK (creator_exact_weight >= creator_match_weight)
);

CREATE TABLE discovery_ranking_state (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  active_revision_id uuid NOT NULL UNIQUE REFERENCES discovery_ranking_revisions(id),
  version integer NOT NULL CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

WITH initial AS (
  INSERT INTO discovery_ranking_revisions(
    version,name,title_exact_weight,title_prefix_weight,title_contains_weight,
    creator_exact_weight,creator_match_weight,body_match_weight,secondary_match_weight,
    recency_weight,creator_activity_weight,work_type_boost,creator_type_boost,product_type_boost,demand_type_boost,
    reason
  ) VALUES (
    1,'Balanced public relevance',100,80,60,50,35,25,12,10,15,0,0,0,0,
    'Initial revision preserves the verified public search ranking behavior.'
  ) RETURNING id
)
INSERT INTO discovery_ranking_state(singleton,active_revision_id,version)
SELECT true,id,1 FROM initial;

CREATE FUNCTION reject_discovery_ranking_revision_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'discovery ranking revisions are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER discovery_ranking_revisions_immutable BEFORE UPDATE OR DELETE ON discovery_ranking_revisions
  FOR EACH ROW EXECUTE FUNCTION reject_discovery_ranking_revision_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('admin:ranking','admin','Manage versioned public discovery ranking policy','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('admin','admin:ranking')
ON CONFLICT DO NOTHING;
