DELETE FROM role_permissions WHERE permission_id='admin:observability';
DELETE FROM permissions WHERE id='admin:observability';
DROP TABLE IF EXISTS request_observations;
DROP TRIGGER IF EXISTS audit_events_immutable ON audit_events;
DROP TRIGGER IF EXISTS audit_events_chain ON audit_events;
DROP FUNCTION IF EXISTS reject_audit_event_mutation();
DROP FUNCTION IF EXISTS append_audit_event_chain();
DROP TABLE IF EXISTS audit_chain_state;
ALTER TABLE audit_events
  DROP CONSTRAINT IF EXISTS audit_events_previous_hash_format,
  DROP CONSTRAINT IF EXISTS audit_events_hash_format,
  DROP CONSTRAINT IF EXISTS audit_events_hash_unique,
  DROP CONSTRAINT IF EXISTS audit_events_sequence_unique,
  DROP COLUMN IF EXISTS event_hash,
  DROP COLUMN IF EXISTS previous_hash,
  DROP COLUMN IF EXISTS sequence;
DROP FUNCTION IF EXISTS audit_event_hash(bigint,text,uuid,uuid,text,text,uuid,text,text,jsonb,timestamptz);
