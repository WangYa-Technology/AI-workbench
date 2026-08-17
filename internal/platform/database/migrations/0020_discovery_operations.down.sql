DROP TRIGGER IF EXISTS discovery_ranking_evaluations_immutable ON discovery_ranking_evaluations;
DROP TRIGGER IF EXISTS discovery_index_runs_immutable ON discovery_index_runs;
DROP FUNCTION IF EXISTS reject_discovery_operations_mutation();
DROP TABLE IF EXISTS discovery_ranking_evaluations;
DROP TABLE IF EXISTS discovery_index_runs;

ALTER TABLE discovery_ranking_state
  DROP CONSTRAINT IF EXISTS discovery_ranking_rollout_candidate,
  DROP CONSTRAINT IF EXISTS discovery_ranking_distinct_candidate,
  DROP COLUMN IF EXISTS rollout_started_at,
  DROP COLUMN IF EXISTS rollout_version,
  DROP COLUMN IF EXISTS rollout_percent,
  DROP COLUMN IF EXISTS candidate_revision_id;

DROP INDEX IF EXISTS users_discovery_identity_pattern_idx;
DROP INDEX IF EXISTS demands_discovery_title_pattern_idx;
DROP INDEX IF EXISTS products_discovery_title_pattern_idx;
DROP INDEX IF EXISTS works_discovery_summary_pattern_idx;
DROP INDEX IF EXISTS works_discovery_title_pattern_idx;
