-- Failure-evidence compensation checks for an existing audit event by action
-- and resource. Keep this lookup independent from the global chain sequence.
CREATE INDEX audit_events_action_resource_idx
  ON audit_events(action, resource_id)
  WHERE resource_id IS NOT NULL;
