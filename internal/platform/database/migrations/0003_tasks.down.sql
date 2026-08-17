DROP INDEX IF EXISTS ledger_task_entry_unique;
DROP TABLE IF EXISTS task_commands;
DROP TABLE IF EXISTS task_settlements;
DROP INDEX IF EXISTS task_disputes_single_open;
DROP TABLE IF EXISTS task_disputes;
DROP TABLE IF EXISTS task_events;

DROP INDEX IF EXISTS deliveries_single_active_review;
DROP INDEX IF EXISTS deliveries_creator_idempotency_key;
ALTER TABLE deliveries
  DROP COLUMN IF EXISTS accepted_at,
  DROP COLUMN IF EXISTS reviewed_at,
  DROP COLUMN IF EXISTS review_note,
  DROP COLUMN IF EXISTS idempotency_key,
  DROP COLUMN IF EXISTS version;

DROP INDEX IF EXISTS proposals_creator_idempotency_key;
ALTER TABLE proposals
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS idempotency_key,
  DROP COLUMN IF EXISTS timeline_days,
  DROP COLUMN IF EXISTS deliverables;

DROP INDEX IF EXISTS demands_assignee_idx;
DROP INDEX IF EXISTS demands_client_idx;
DROP INDEX IF EXISTS demands_marketplace_idx;
DROP INDEX IF EXISTS demands_client_idempotency_key;
DROP INDEX IF EXISTS generations_source_task_idx;
ALTER TABLE generations DROP COLUMN IF EXISTS source_task_id;
ALTER TABLE demands
  DROP COLUMN IF EXISTS cancelled_at,
  DROP COLUMN IF EXISTS accepted_at,
  DROP COLUMN IF EXISTS idempotency_key,
  DROP COLUMN IF EXISTS allow_direct_accept,
  DROP COLUMN IF EXISTS client_timezone,
  DROP COLUMN IF EXISTS ai_disclosure_requirement,
  DROP COLUMN IF EXISTS rights_terms,
  DROP COLUMN IF EXISTS acceptance_rules,
  DROP COLUMN IF EXISTS deliverables,
  DROP COLUMN IF EXISTS summary;
