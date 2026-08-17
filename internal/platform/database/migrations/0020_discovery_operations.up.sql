CREATE INDEX works_discovery_title_pattern_idx ON works (lower(title) text_pattern_ops) WHERE status='published';
CREATE INDEX works_discovery_summary_pattern_idx ON works (lower(summary) text_pattern_ops) WHERE status='published';
CREATE INDEX products_discovery_title_pattern_idx ON products (lower(title) text_pattern_ops) WHERE status='active';
CREATE INDEX demands_discovery_title_pattern_idx ON demands (lower(title) text_pattern_ops) WHERE status='open';
CREATE INDEX users_discovery_identity_pattern_idx ON users (lower(handle) text_pattern_ops, lower(display_name) text_pattern_ops) WHERE status='active';

ALTER TABLE discovery_ranking_state
  ADD COLUMN candidate_revision_id uuid REFERENCES discovery_ranking_revisions(id),
  ADD COLUMN rollout_percent integer NOT NULL DEFAULT 0 CHECK (rollout_percent IN (0,5,10,25,50,100)),
  ADD COLUMN rollout_version integer NOT NULL DEFAULT 1 CHECK (rollout_version > 0),
  ADD COLUMN rollout_started_at timestamptz,
  ADD CONSTRAINT discovery_ranking_distinct_candidate CHECK (candidate_revision_id IS NULL OR candidate_revision_id <> active_revision_id),
  ADD CONSTRAINT discovery_ranking_rollout_candidate CHECK (rollout_percent = 0 OR candidate_revision_id IS NOT NULL);

CREATE TABLE discovery_index_runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation text NOT NULL CHECK (operation IN ('analyze')),
  status text NOT NULL CHECK (status IN ('succeeded','failed')),
  document_counts jsonb NOT NULL,
  index_sizes jsonb NOT NULL,
  error_code text,
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 500),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  started_at timestamptz NOT NULL,
  completed_at timestamptz NOT NULL CHECK (completed_at >= started_at)
);

CREATE INDEX discovery_index_runs_created_idx ON discovery_index_runs(completed_at DESC,id DESC);

CREATE TABLE discovery_ranking_evaluations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  candidate_revision_id uuid NOT NULL REFERENCES discovery_ranking_revisions(id),
  baseline_revision_id uuid NOT NULL REFERENCES discovery_ranking_revisions(id),
  status text NOT NULL CHECK (status IN ('passed','failed')),
  case_count integer NOT NULL CHECK (case_count > 0 AND case_count <= 100),
  candidate_top1_hits integer NOT NULL CHECK (candidate_top1_hits BETWEEN 0 AND case_count),
  baseline_top1_hits integer NOT NULL CHECK (baseline_top1_hits BETWEEN 0 AND case_count),
  candidate_mrr numeric(8,6) NOT NULL CHECK (candidate_mrr BETWEEN 0 AND 1),
  baseline_mrr numeric(8,6) NOT NULL CHECK (baseline_mrr BETWEEN 0 AND 1),
  safety_violations integer NOT NULL CHECK (safety_violations >= 0),
  metrics jsonb NOT NULL,
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 500),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX discovery_ranking_evaluations_candidate_idx ON discovery_ranking_evaluations(candidate_revision_id,created_at DESC);

CREATE FUNCTION reject_discovery_operations_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'discovery operations evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER discovery_index_runs_immutable BEFORE UPDATE OR DELETE ON discovery_index_runs
  FOR EACH ROW EXECUTE FUNCTION reject_discovery_operations_mutation();
CREATE TRIGGER discovery_ranking_evaluations_immutable BEFORE UPDATE OR DELETE ON discovery_ranking_evaluations
  FOR EACH ROW EXECUTE FUNCTION reject_discovery_operations_mutation();
